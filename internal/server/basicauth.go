package server

import (
	"math"
	"net/http"
	"strconv"

	"golang.org/x/crypto/bcrypt"

	"github.com/giangdinhtt/pixbowl/internal/id"
)

// requireUI protects the upload page. Browsers can only answer a Basic Auth
// challenge, so an upload token alone leaves the page itself public; the page
// then asks for the token before uploading.
func (s *Server) requireUI(next http.HandlerFunc) http.HandlerFunc {
	if !s.cfg.BasicAuthEnabled() {
		return next
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if s.checkBasicAuth(r) {
			next(w, r)
			return
		}
		s.rejectUnauthorized(w, r, true)
	}
}

// requireUpload protects the upload endpoint. It accepts either the Basic Auth
// pair (humans in a browser) or UPLOAD_TOKEN as a bearer token (scripts). The
// upload token is deliberately useless for deletes.
func (s *Server) requireUpload(next http.HandlerFunc) http.HandlerFunc {
	if !s.cfg.BasicAuthEnabled() && s.cfg.UploadToken == "" {
		return next
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.BasicAuthEnabled() && s.checkBasicAuth(r) {
			next(w, r)
			return
		}
		if s.cfg.UploadToken != "" && id.Equal(bearerToken(r), s.cfg.UploadToken) {
			next(w, r)
			return
		}
		s.rejectUnauthorized(w, r, s.cfg.BasicAuthEnabled())
	}
}

// rejectUnauthorized rate limits failures and answers 401. The Basic challenge
// is only advertised when Basic Auth is actually configured; otherwise the
// browser would prompt for credentials that cannot work.
func (s *Server) rejectUnauthorized(w http.ResponseWriter, r *http.Request, challenge bool) {
	// Failed logins share the delete-token limiter: both are credential
	// guessing and should cost the same budget.
	if allowed, retry := s.authLimiter.Allow(s.clientIP(r)); !allowed {
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(retry.Seconds()))))
		s.writeError(w, http.StatusTooManyRequests, "rate_limited", "Too many failed logins, try again later.")
		return
	}

	if challenge {
		w.Header().Set("WWW-Authenticate", `Basic realm="pixbowl", charset="UTF-8"`)
		w.Header().Set("Cache-Control", "no-store")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	s.writeError(w, http.StatusUnauthorized, "unauthorized", "A valid upload token is required.")
}

func (s *Server) checkBasicAuth(r *http.Request) bool {
	user, pass, ok := r.BasicAuth()
	if !ok {
		return false
	}
	if !id.Equal(user, s.cfg.BasicAuthUser) {
		return false
	}
	// bcrypt is deliberately slow; a failed username check above keeps
	// unauthenticated requests from burning CPU on hashing.
	return bcrypt.CompareHashAndPassword([]byte(s.cfg.BasicAuthHash), []byte(pass)) == nil
}
