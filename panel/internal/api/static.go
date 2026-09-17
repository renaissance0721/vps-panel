package api

import (
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

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
