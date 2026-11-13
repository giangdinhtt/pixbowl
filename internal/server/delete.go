package server

import (
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/giangdinhtt/pixbowl/internal/id"
	"github.com/giangdinhtt/pixbowl/internal/store"
)

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	imageID, ok := imageIDFromName(r.PathValue("name"))
	if !ok {
		s.notFound(w)
		return
	}

	meta, err := s.store.Meta(r.Context(), imageID)
	if err != nil {
		s.storeError(w, err)
		return
	}

	if !s.authorizedToDelete(r, meta) {
		// Count only failures so a legitimate uploader deleting several images
		// is never throttled, while guessing tokens is.
		if allowed, retry := s.authLimiter.Allow(s.clientIP(r)); !allowed {
			w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(retry.Seconds()))))
			s.writeError(w, http.StatusTooManyRequests, "rate_limited", "Too many failed attempts, try again later.")
			return
		}
		s.writeError(w, http.StatusUnauthorized, "unauthorized", "A delete token (X-Delete-Token header) or the admin token is required.")
		return
	}

	if err := s.store.Delete(r.Context(), imageID); err != nil {
		s.storeError(w, err)
		return
	}
	s.log.Info("image.deleted", "id", imageID)
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "deleted", "id": imageID})
}

func (s *Server) authorizedToDelete(r *http.Request, meta *store.Meta) bool {
	if s.cfg.AdminToken != "" {
		if token := bearerToken(r); token != "" && id.Equal(token, s.cfg.AdminToken) {
			return true
		}
	}

	token := strings.TrimSpace(r.Header.Get("X-Delete-Token"))
	if token == "" {
		token = strings.TrimSpace(r.URL.Query().Get("token"))
	}
	if token == "" || meta.DeleteTokenHash == "" {
		return false
	}
	return id.Equal(sha256Hex([]byte(token)), meta.DeleteTokenHash)
}

func bearerToken(r *http.Request) string {
	header := r.Header.Get("Authorization")
	if len(header) > 7 && strings.EqualFold(header[:7], "bearer ") {
		return strings.TrimSpace(header[7:])
	}
	return ""
}
