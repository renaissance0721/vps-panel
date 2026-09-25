package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/auth"
	landingstore "github.com/renaissance0721/vps-panel/panel/internal/landing"
)

type createLandingRequest struct {
	Name       string `json:"name"`
	Visibility string `json:"visibility"`
	URI        string `json:"uri"`
}

type updateLandingRequest struct {
	Name       *string `json:"name"`
	Visibility *string `json:"visibility"`
	URI        *string `json:"uri"`
}

type landingResponse struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	Visibility string    `json:"visibility"`
	Protocol   string    `json:"protocol"`
	Host       string    `json:"host"`
	Port       int       `json:"port"`
	OwnedByMe  bool      `json:"owned_by_me"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func (s *server) listLandings(w http.ResponseWriter, r *http.Request, user auth.User) {
	values, err := s.landings.List(r.Context(), user.ID)
	if err != nil {
		writeInternalError(w)
		return
	}
	response := make([]landingResponse, 0, len(values))
	for _, value := range values {
		response = append(response, toLandingResponse(value))
	}
	writeJSON(w, http.StatusOK, map[string]any{"landings": response})
}

func (s *server) createLanding(w http.ResponseWriter, r *http.Request, user auth.User) {
	var request createLandingRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	value, err := s.landings.Create(r.Context(), user.ID, landingstore.CreateInput{
		Name: request.Name, Visibility: request.Visibility, URI: request.URI,
	})
	if err != nil {
		writeLandingError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"landing": toLandingResponse(value)})
}

func (s *server) getLanding(w http.ResponseWriter, r *http.Request, user auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "外部节点 ID 无效")
	if !ok {
		return
	}
	value, err := s.landingForUser(r.Context(), user, id)
	if err != nil {
		writeLandingError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"landing": toLandingResponse(value)})
}

func (s *server) updateLanding(w http.ResponseWriter, r *http.Request, user auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "外部节点 ID 无效")
	if !ok {
		return
	}
	var request updateLandingRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	value, endpointChanged, err := s.landings.Update(r.Context(), id, user.ID, landingstore.UpdateInput{
		Name: request.Name, Visibility: request.Visibility, URI: request.URI,
	})
	if err != nil {
		writeLandingError(w, err)
		return
	}
	if endpointChanged {
		mutations, err := s.relays.BumpForLandingTarget(r.Context(), id)
		if err != nil {
			writeInternalError(w)
			return
		}
		s.notifyRelayMutations(mutations)
	}
	writeJSON(w, http.StatusOK, map[string]any{"landing": toLandingResponse(value)})
}

func (s *server) deleteLanding(w http.ResponseWriter, r *http.Request, user auth.User) {
	id, ok := readPositiveID(w, r.PathValue("id"), "外部节点 ID 无效")
	if !ok {
		return
	}
	if err := s.landings.Delete(r.Context(), id, user.ID); err != nil {
		writeLandingError(w, err)
		return
	}
	writeNoContent(w)
}

func toLandingResponse(value landingstore.Landing) landingResponse {
	return landingResponse{
		ID: value.ID, Name: value.Name, Visibility: value.Visibility, Protocol: value.Protocol,
		Host: value.Host, Port: value.Port, OwnedByMe: value.OwnedByMe,
		CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

func writeLandingError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, landingstore.ErrNotFound):
		writeError(w, http.StatusNotFound, "外部节点不存在")
	case errors.Is(err, landingstore.ErrInvalidName):
		writeError(w, http.StatusBadRequest, "名称不能为空且不能超过 100 个字符")
	case errors.Is(err, landingstore.ErrInvalidVisibility):
		writeError(w, http.StatusBadRequest, "可见性仅支持私有或公开")
	case errors.Is(err, landingstore.ErrUnsupportedProtocol):
		writeError(w, http.StatusBadRequest, "当前仅支持导入 VLESS 和 Shadowsocks 外部节点")
	case errors.Is(err, landingstore.ErrUnsupportedVLESSTransport):
		writeError(w, http.StatusBadRequest, "当前仅支持导入 TCP VLESS 外部节点")
	case errors.Is(err, landingstore.ErrUnsupportedSSPlugin):
		writeError(w, http.StatusBadRequest, "当前暂不支持带 plugin 的 Shadowsocks 外部节点")
	case errors.Is(err, landingstore.ErrImmutableProtocol):
		writeError(w, http.StatusConflict, "外部节点协议创建后不能修改")
	case errors.Is(err, landingstore.ErrReferencedByRelay):
		writeError(w, http.StatusConflict, "该外部节点正在被中转使用，请先修改或删除相关中转")
	case errors.Is(err, landingstore.ErrInvalidURI):
		writeError(w, http.StatusBadRequest, "外部节点链接无效")
	default:
		writeInternalError(w)
	}
}
