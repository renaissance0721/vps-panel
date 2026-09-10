package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

type server struct {
	db      *sql.DB
	webRoot string
}

func NewHandler(db *sql.DB, webRoot string) http.Handler {
	s := &server{db: db, webRoot: webRoot}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.health)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	mux.Handle("/", s.spa())
	return mux
}

func (s *server) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	if err := s.db.PingContext(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"status":   "error",
			"database": "unavailable",
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status":   "ok",
		"database": "ok",
	})
}

func (s *server) spa() http.Handler {
	files := http.FileServer(http.Dir(s.webRoot))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		cleanPath := filepath.FromSlash(strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/"))
		if cleanPath != "." {
			requestedFile := filepath.Join(s.webRoot, cleanPath)
			if info, err := os.Stat(requestedFile); err == nil && !info.IsDir() {
				files.ServeHTTP(w, r)
				return
			}
		}

		indexPath := filepath.Join(s.webRoot, "index.html")
		if _, err := os.Stat(indexPath); err != nil {
			http.Error(w, "Panel web files are unavailable.", http.StatusServiceUnavailable)
			return
		}

		http.ServeFile(w, r, indexPath)
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
