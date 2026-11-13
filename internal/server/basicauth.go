package server

import (
	"math"
	"net/http"
	"strconv"

	"golang.org/x/crypto/bcrypt"

	"github.com/giangdinhtt/pixbowl/internal/id"
)

// requireBasicAuth guards the upload surface (page + endpoint) with HTTP Basic
// Auth. When BASIC_AUTH_USER/BASIC_AUTH_PASS are not configured it is a no-op,
// so existing deployments keep working. Public image links are never guarded.
func (s *Server) requireBasicAuth(next http.HandlerFunc) http.HandlerFunc {
	if !s.cfg.BasicAuthEnabled() {
		return next
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if s.checkBasicAuth(r) {
			next(w, r)
			return
		}

		// Failed logins share the delete-token limiter: both are credential
		// guessing and should cost the same budget.
		if allowed, retry := s.authLimiter.Allow(s.clientIP(r)); !allowed {
			w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(retry.Seconds()))))
			s.writeError(w, http.StatusTooManyRequests, "rate_limited", "Quá nhiều lần đăng nhập sai, thử lại sau.")
			return
		}

		w.Header().Set("WWW-Authenticate", `Basic realm="pixbowl", charset="UTF-8"`)
		w.Header().Set("Cache-Control", "no-store")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}
}

func (s *Server) checkBasicAuth(r *http.Request) bool {
	user, pass, ok := r.BasicAuth()
	if !ok {
		return false
	}
	if !id.Equal(user, s.cfg.BasicAuthUser) {
		return false
	}
	if s.cfg.BasicAuthHash != "" {
		// bcrypt is deliberately slow; a failed username check above keeps
		// unauthenticated requests from burning CPU on hashing.
		return bcrypt.CompareHashAndPassword([]byte(s.cfg.BasicAuthHash), []byte(pass)) == nil
	}
	return id.Equal(pass, s.cfg.BasicAuthSecret)
}
