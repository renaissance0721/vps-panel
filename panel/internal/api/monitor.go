package api

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"

	"github.com/renaissance0721/vps-panel/panel/internal/agentcontrol"
	"github.com/renaissance0721/vps-panel/panel/internal/auth"
	"github.com/renaissance0721/vps-panel/panel/internal/monitor"
)

func (s *server) listProbes(w http.ResponseWriter, r *http.Request, user auth.User) {
	values, err := s.monitor.List(r.Context())
	if err != nil {
		writeMonitorError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tasks": values})
}

func (s *server) saveProbe(w http.ResponseWriter, r *http.Request, user auth.User) {
	var id int64
	if r.Method == http.MethodPatch {
		var ok bool
		id, ok = readPositiveID(w, r.PathValue("id"), "探测任务 ID 无效")
		if !ok {
			return
		}
	}
	var request struct {
		Name            *string         `json:"name"`
		Type            *string         `json:"type"`
		Target          *string         `json:"target"`
		Port            json.RawMessage `json:"port"`
		IntervalSeconds *int            `json:"interval_seconds"`
		Enabled         *bool           `json:"enabled"`
		ServerIDs       *[]int64        `json:"server_ids"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	value := monitor.Task{ProbeTask: monitor.ProbeTask{IntervalSeconds: 60}, Enabled: true}
	// Serialize PATCH reads and writes as well as the full-list push.
	s.probeMu.Lock()
	defer s.probeMu.Unlock()
	if id != 0 {
		values, err := s.monitor.List(r.Context())
		if err != nil {
			writeMonitorError(w, err)
			return
		}
		found := false
		for _, item := range values {
			if item.ID == id {
				value = item
				found = true
				break
			}
		}
		if !found {
			writeMonitorError(w, monitor.ErrNotFound)
			return
		}
	}
	if request.Name != nil {
		value.Name = *request.Name
	}
	if request.Type != nil {
		value.Type = *request.Type
	}
	if request.Target != nil {
		value.Target = *request.Target
	}
	if request.Port != nil {
		if json.Unmarshal(request.Port, &value.Port) != nil {
			writeMonitorError(w, monitor.ErrInvalid)
			return
		}
	}
	if request.IntervalSeconds != nil {
		value.IntervalSeconds = *request.IntervalSeconds
	}
	if request.Enabled != nil {
		value.Enabled = *request.Enabled
	}
	if request.ServerIDs != nil {
		value.ServerIDs = *request.ServerIDs
	}
	err := s.agents.WithProbeCapabilities(func(capabilities map[int64]map[string]bool) error {
		var err error
		value, err = s.monitor.Save(r.Context(), id, value, capabilities)
		return err
	})
	if err != nil {
		writeMonitorError(w, err)
		return
	}
	s.notifyProbeTasks()
	code := http.StatusOK
	if id == 0 {
		code = http.StatusCreated
	}
	writeJSON(w, code, map[string]any{"task": value})
}

func (s *server) deleteProbe(w http.ResponseWriter, r *http.Request, user auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "探测任务 ID 无效")
	if !ok {
		return
	}
	s.probeMu.Lock()
	defer s.probeMu.Unlock()
	if err := s.monitor.Delete(r.Context(), id); err != nil {
		writeMonitorError(w, err)
		return
	}
	s.notifyProbeTasks()
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) notifyProbeTasks() {
	for _, serverID := range s.agents.ProbeConnectionIDs() {
		if err := s.agents.NotifyProbeTasks(serverID, s.monitor); err != nil {
			log.Printf("notify probe tasks for server %d: %v", serverID, err)
		}
	}
}

func (s *server) getMonitorLatency(w http.ResponseWriter, r *http.Request, user auth.User) {
	id, ok := readPositiveID(w, r.PathValue("server_id"), "服务器 ID 无效")
	if !ok || !s.requireServerAccess(w, r, user, id) {
		return
	}
	hours := 6
	if raw, present := r.URL.Query()["hours"]; present {
		if len(raw) != 1 {
			writeMonitorError(w, monitor.ErrInvalid)
			return
		}
		parsed, err := strconv.Atoi(raw[0])
		if err != nil {
			writeMonitorError(w, monitor.ErrInvalid)
			return
		}
		hours = parsed
	}
	value, err := s.servers.Get(r.Context(), id)
	if err != nil {
		writeServerError(w, err)
		return
	}
	capabilities := make(map[string]bool)
	// Last explicitly reported metadata also allows viewing history while offline.
	for _, capability := range value.AgentCapabilities {
		capabilities[capability] = true
	}
	history, err := s.monitor.History(r.Context(), id, hours, capabilities)
	if err != nil {
		writeMonitorError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, history)
}

func (s *server) reportCurrentProbe(serverID int64, connection *agentcontrol.Connection, result monitor.ProbeResult) (bool, error) {
	return s.agents.WithCurrentConnection(serverID, connection, func(ctx context.Context) error {
		return s.monitor.Ingest(ctx, serverID, connection.Capabilities, result)
	})
}

func writeMonitorError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, monitor.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "探测任务不存在"})
	case errors.Is(err, monitor.ErrInvalid), errors.Is(err, monitor.ErrUnsupported), errors.Is(err, monitor.ErrTaskLimit):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	default:
		writeInternalError(w, err)
	}
}
