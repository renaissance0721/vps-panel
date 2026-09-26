package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
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

func writeInternalError(w http.ResponseWriter) {
	writeError(w, http.StatusInternalServerError, "服务器内部错误")
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
