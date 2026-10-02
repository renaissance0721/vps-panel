package api

import (
	"errors"
	"net/http"

	"github.com/renaissance0721/vps-panel/panel/internal/auth"
	"github.com/renaissance0721/vps-panel/panel/internal/notification"
)

func (s *server) getNotificationSettings(w http.ResponseWriter, r *http.Request, _ auth.User) {
	value, err := s.notifications.Settings(r.Context())
	if err != nil {
		writeInternalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func (s *server) saveNotificationSettings(w http.ResponseWriter, r *http.Request, _ auth.User) {
	current, err := s.notifications.Settings(r.Context())
	if err != nil {
		writeInternalError(w, err)
		return
	}
	input := notification.Update{Settings: current}
	if !decodeJSON(w, r, &input) {
		return
	}
	if err := s.notifications.Save(r.Context(), input); err != nil {
		writeNotificationError(w, err)
		return
	}
	s.getNotificationSettings(w, r, auth.User{})
}

func (s *server) testNotification(w http.ResponseWriter, r *http.Request, _ auth.User) {
	var input notification.TestInput
	if !decodeJSON(w, r, &input) {
		return
	}
	result, err := s.notifications.SendTest(r.Context(), input)
	if err != nil {
		writeNotificationError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, result)
}

func (s *server) getNotificationTest(w http.ResponseWriter, r *http.Request, _ auth.User) {
	result, err := s.notifications.TestResult(r.PathValue("id"))
	if err != nil {
		writeNotificationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func writeNotificationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, notification.ErrInvalidSettings):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, notification.ErrTestBusy):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, notification.ErrQueueFull):
		writeError(w, http.StatusServiceUnavailable, err.Error())
	case errors.Is(err, notification.ErrTestNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	default:
		writeInternalError(w, err)
	}
}
