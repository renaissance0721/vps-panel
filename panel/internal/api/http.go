package api

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func secureRequest(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	forwardedProtocol := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0])
	return strings.EqualFold(forwardedProtocol, "https")
}

func requestBaseURL(r *http.Request) string {
	scheme := "http"
	if secureRequest(r) {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func (s *server) panelBaseURL(r *http.Request) (string, bool) {
	domain := s.backup.Domain
	if domain != "" && domain != ":80" {
		if !validPanelDomain(domain) {
			return "", false
		}
		return "https://" + domain, true
	}
	if !validRequestHost(r.Host) {
		return "", false
	}
	return requestBaseURL(r), true
}

func validPanelDomain(domain string) bool {
	return strings.TrimSpace(domain) == domain && strings.Contains(domain, ".") && validDNSHostname(domain)
}

func validRequestHost(host string) bool {
	if host == "" || strings.TrimSpace(host) != host || strings.ContainsAny(host, "/?#@\\") {
		return false
	}
	parsed, err := url.Parse("http://" + host)
	if err != nil || parsed.Host != host || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	hostname := parsed.Hostname()
	if hostname == "" {
		return false
	}
	if _, err := netip.ParseAddr(hostname); err != nil && !validDNSHostname(hostname) {
		return false
	}
	if port := parsed.Port(); port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value <= 0 || value > 65535 {
			return false
		}
	}
	return true
}

func validDNSHostname(hostname string) bool {
	if len(hostname) == 0 || len(hostname) > 253 {
		return false
	}
	for _, label := range strings.Split(hostname, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') &&
				(character < '0' || character > '9') && character != '-' {
				return false
			}
		}
	}
	return true
}

func isUnsafeSessionMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func (s *server) validateSessionRequestOrigin(r *http.Request) bool {
	expected, ok := s.panelBaseURL(r)
	if !ok {
		return false
	}
	source := r.Header.Get("Origin")
	referer := false
	if source == "" {
		source = r.Header.Get("Referer")
		referer = true
	}
	if source == "" || source == "null" {
		return false
	}
	expectedURL, err := url.Parse(expected)
	if err != nil {
		return false
	}
	sourceURL, err := url.Parse(source)
	if err != nil || sourceURL.User != nil || sourceURL.Scheme == "" || sourceURL.Host == "" {
		return false
	}
	if !referer && (sourceURL.Path != "" || sourceURL.RawQuery != "" || sourceURL.Fragment != "") {
		return false
	}
	return strings.EqualFold(sourceURL.Scheme, expectedURL.Scheme) && strings.EqualFold(sourceURL.Host, expectedURL.Host)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, destination any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		writeError(w, http.StatusBadRequest, "请求内容无效")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "请求内容无效")
		return false
	}
	return true
}

func readPositiveID(w http.ResponseWriter, value, errorMessage string) (int64, bool) {
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, errorMessage)
		return 0, false
	}
	return id, true
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

type requestContextKey string

const requestIDContextKey requestContextKey = "request_id"

var errPanelBaseURL = errors.New("panel base URL is invalid")

type observedResponseWriter struct {
	http.ResponseWriter
	status        int
	internalError error
}

func (w *observedResponseWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *observedResponseWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(data)
}

func (w *observedResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *observedResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("response writer does not support hijacking")
	}
	return hijacker.Hijack()
}

func (w *observedResponseWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *observedResponseWriter) Push(target string, options *http.PushOptions) error {
	pusher, ok := w.ResponseWriter.(http.Pusher)
	if !ok {
		return http.ErrNotSupported
	}
	return pusher.Push(target, options)
}

func requestMetadataMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := newRequestID()
		r = r.WithContext(context.WithValue(r.Context(), requestIDContextKey, requestID))
		w.Header().Set("X-Request-ID", requestID)
		observed := &observedResponseWriter{ResponseWriter: w}
		next.ServeHTTP(observed, r)
		if observed.status == http.StatusInternalServerError {
			actorID := int64(0)
			if actor, ok := actorFromContext(r.Context()); ok {
				actorID = actor.ID
			}
			route := r.Pattern
			if route == "" {
				route = r.URL.Path
			}
			log.Printf("API internal error request_id=%s method=%s route=%s actor_user_id=%d error=%v",
				requestID, r.Method, route, actorID, observed.internalError)
		}
	})
}

func newRequestID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err == nil {
		return fmt.Sprintf("%x", value[:])
	}
	return fmt.Sprintf("fallback-%d", time.Now().UnixNano())
}

func requestIDFromContext(ctx context.Context) string {
	value, _ := ctx.Value(requestIDContextKey).(string)
	return value
}

func writeInternalError(w http.ResponseWriter, values ...any) {
	var internalErr error
	for _, value := range values {
		if err, ok := value.(error); ok {
			internalErr = err
		}
	}
	if internalErr == nil {
		internalErr = errors.New("unspecified internal error")
	}
	if observed, ok := w.(*observedResponseWriter); ok {
		observed.internalError = internalErr
	}
	writeJSON(w, http.StatusInternalServerError, map[string]string{
		"error": "服务器内部错误", "request_id": w.Header().Get("X-Request-ID"),
	})
}

func writeNoContent(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
