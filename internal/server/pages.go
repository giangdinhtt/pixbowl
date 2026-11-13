package server

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/giangdinhtt/pixbowl/internal/id"
	"github.com/giangdinhtt/pixbowl/internal/imageproc"
)

func (s *Server) handleIndex(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", contentSecurityPolicy)
	_, _ = io.WriteString(w, s.indexPage)
}

func (s *Server) handleDeletePage(w http.ResponseWriter, r *http.Request) {
	imageID := r.PathValue("id")
	if !id.Valid(imageID) {
		s.notFound(w)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", contentSecurityPolicy)
	_, _ = io.WriteString(w, strings.ReplaceAll(s.deletePage, "{{ID}}", imageID))
}

func (s *Server) handleLiveness(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(w, "ok\n")
}

func (s *Server) handleRobots(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, "User-agent: *\nDisallow: /\n")
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	status := "ok"
	code := http.StatusOK
	if err := s.store.Healthy(ctx); err != nil {
		status = "degraded"
		code = http.StatusServiceUnavailable
		s.log.Error("health.storage", "err", err)
	}

	s.writeJSON(w, code, map[string]any{
		"status":  status,
		"storage": s.cfg.Storage,
		"libvips": imageproc.Version(),
		"formats": imageproc.Formats(),
	})
}
