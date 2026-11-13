package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/giangdinhtt/pixbowl/internal/config"
	"github.com/giangdinhtt/pixbowl/internal/id"
	"github.com/giangdinhtt/pixbowl/internal/imageproc"
	"github.com/giangdinhtt/pixbowl/internal/ratelimit"
	"github.com/giangdinhtt/pixbowl/internal/store"
	"github.com/giangdinhtt/pixbowl/web"
)

const contentSecurityPolicy = "default-src 'none'; img-src 'self' data: blob:; style-src 'unsafe-inline'; script-src 'unsafe-inline'; connect-src 'self'; base-uri 'none'; form-action 'none'"

// Server wires configuration, storage and the HTTP routes together.
type Server struct {
	cfg   *config.Config
	store store.Store
	log   *slog.Logger

	uploadLimiter *ratelimit.Limiter
	authLimiter   *ratelimit.Limiter

	uploadSem  chan struct{}
	variantSem chan struct{}
	waiters    atomic.Int64

	inflight flightGroup

	indexPage  string
	deletePage string

	stop      chan struct{}
	closeOnce sync.Once
}

func New(cfg *config.Config, st store.Store, log *slog.Logger) *Server {
	s := &Server{
		cfg:           cfg,
		store:         st,
		log:           log,
		uploadLimiter: ratelimit.New(ratelimit.Limits{Minute: cfg.RateLimit.Minute, Hour: cfg.RateLimit.Hour, Day: cfg.RateLimit.Day}),
		authLimiter:   ratelimit.New(ratelimit.Limits{Minute: cfg.AuthAttemptsPerMinute}),
		uploadSem:     make(chan struct{}, cfg.MaxConcurrentUploads),
		variantSem:    make(chan struct{}, cfg.MaxConcurrentVariants),
		indexPage:     buildIndexPage(cfg),
		deletePage:    web.DeleteHTML,
		stop:          make(chan struct{}),
	}
	go s.cleanupLoop()
	return s
}

// Close stops the background janitor. Safe to call more than once.
func (s *Server) Close() {
	s.closeOnce.Do(func() { close(s.stop) })
}

func (s *Server) cleanupLoop() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-ticker.C:
			s.uploadLimiter.Cleanup()
			s.authLimiter.Cleanup()
		}
	}
}

// Handler returns the fully wrapped HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.HandleFunc("POST /{$}", s.handleUpload)
	mux.HandleFunc("POST /upload", s.handleUpload)

	mux.HandleFunc("GET /i/{name}", s.handleImage)
	mux.HandleFunc("GET /o/{name}", s.handleOriginal)
	mux.HandleFunc("DELETE /i/{name}", s.handleDelete)
	mux.HandleFunc("GET /d/{id}", s.handleDeletePage)

	mux.HandleFunc("GET /liveness", s.handleLiveness)
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /robots.txt", s.handleRobots)
	mux.HandleFunc("GET /favicon.ico", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("OPTIONS /", s.handlePreflight)

	return s.wrap(mux)
}

func (s *Server) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}

		defer func() {
			if recovered := recover(); recovered != nil {
				s.log.Error("panic", "value", recovered, "path", r.URL.Path)
				if !sw.wrote {
					s.writeError(sw, http.StatusInternalServerError, "internal", "Có lỗi xảy ra.")
				}
			}
			if r.URL.Path != "/liveness" {
				s.log.Debug("request",
					"method", r.Method,
					"path", r.URL.Path,
					"status", sw.status,
					"duration", time.Since(start).String(),
					"ip", s.clientIP(r),
				)
			}
		}()

		s.applyHeaders(sw, r)
		next.ServeHTTP(sw, r)
	})
}

func (s *Server) applyHeaders(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")

	origin := r.Header.Get("Origin")
	if origin == "" {
		return
	}
	// Always vary on Origin so a shared cache cannot serve one origin's
	// CORS decision to another.
	h.Add("Vary", "Origin")
	if allowed, ok := s.allowedOrigin(origin); ok {
		h.Set("Access-Control-Allow-Origin", allowed)
		h.Set("Access-Control-Allow-Methods", "GET, HEAD, POST, DELETE, OPTIONS")
		h.Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Delete-Token")
		h.Set("Access-Control-Max-Age", "86400")
		h.Set("Access-Control-Expose-Headers", "Content-Length, ETag")
	}
}

func (s *Server) allowedOrigin(origin string) (string, bool) {
	for _, allowed := range s.cfg.AllowedOrigins {
		if allowed == "*" {
			return "*", true
		}
		if strings.EqualFold(allowed, origin) {
			return origin, true
		}
	}
	return "", false
}

// handlePreflight rejects cross-origin preflights unless the origin is allowed.
func (s *Server) handlePreflight(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.allowedOrigin(r.Header.Get("Origin")); !ok {
		s.writeError(w, http.StatusForbidden, "cors_denied", "Origin này không được phép gọi API.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func (s *Server) writeError(w http.ResponseWriter, status int, code, message string) {
	s.writeJSON(w, status, map[string]string{"error": code, "message": message})
}

func (s *Server) notFound(w http.ResponseWriter) {
	http.Error(w, "not found", http.StatusNotFound)
}

func (s *Server) storeError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		s.notFound(w)
		return
	}
	s.log.Error("storage", "err", err)
	s.writeError(w, http.StatusInternalServerError, "storage_error", "Lỗi truy cập kho lưu trữ.")
}

func (s *Server) clientIP(r *http.Request) string {
	if s.cfg.TrustProxy {
		if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
			first := strings.TrimSpace(strings.Split(forwarded, ",")[0])
			if first != "" {
				return stripPort(first)
			}
		}
		if realIP := strings.TrimSpace(r.Header.Get("X-Real-IP")); realIP != "" {
			return stripPort(realIP)
		}
	}
	return stripPort(r.RemoteAddr)
}

func stripPort(hostport string) string {
	if host, _, err := net.SplitHostPort(hostport); err == nil {
		return host
	}
	return hostport
}

var hostPattern = regexp.MustCompile(`^[A-Za-z0-9.:\-\[\]]{1,255}$`)

func (s *Server) baseURL(r *http.Request) string {
	if s.cfg.BaseURL != "" {
		return s.cfg.BaseURL
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if s.cfg.TrustProxy {
		if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
			scheme = strings.TrimSpace(strings.Split(proto, ",")[0])
		}
	}
	host := r.Host
	if !hostPattern.MatchString(host) {
		host = "localhost"
	}
	return scheme + "://" + host
}

func imageIDFromName(name string) (string, bool) {
	base := name
	if index := strings.IndexByte(base, '.'); index >= 0 {
		base = base[:index]
	}
	if !id.Valid(base) {
		return "", false
	}
	return base, true
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func sanitizeFilename(name string) string {
	name = strings.TrimSpace(name)
	if index := strings.LastIndexAny(name, `/\`); index >= 0 {
		name = name[index+1:]
	}
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == '"' || r == '\'' {
			return -1
		}
		return r
	}, name)
	if len(name) > 120 {
		name = name[:120]
	}
	return name
}

func etag(sha, suffix string) string {
	if suffix == "" {
		return `"` + sha + `"`
	}
	return `"` + sha + "-" + suffix + `"`
}

func buildIndexPage(cfg *config.Config) string {
	formats := strings.Join(imageproc.Formats(), ", ")
	maxDimension := strconv.Itoa(cfg.MaxDimension)
	if cfg.MaxDimension == 0 {
		maxDimension = "không giới hạn"
	}
	keepOriginal := "có"
	if !cfg.KeepOriginal {
		keepOriginal = "không"
	}
	return strings.NewReplacer(
		"{{MAX_FILE_MB}}", strconv.FormatInt(cfg.MaxFileBytes>>20, 10),
		"{{MAX_FILES}}", strconv.Itoa(cfg.MaxFiles),
		"{{FORMATS}}", formats,
		"{{MAX_DIMENSION}}", maxDimension,
		"{{KEEP_ORIGINAL}}", keepOriginal,
	).Replace(web.IndexHTML)
}

// statusWriter records the status code for request logging.
type statusWriter struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (w *statusWriter) WriteHeader(code int) {
	if w.wrote {
		return
	}
	w.status = code
	w.wrote = true
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(data []byte) (int, error) {
	if !w.wrote {
		w.wrote = true
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(data)
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// flightGroup collapses concurrent generation of the same variant into one job.
type flightGroup struct {
	mu    sync.Mutex
	calls map[string]*flightCall
}

type flightCall struct {
	done   chan struct{}
	data   []byte
	format string
	err    error
}

func (g *flightGroup) Do(key string, fn func() ([]byte, string, error)) ([]byte, string, error) {
	g.mu.Lock()
	if g.calls == nil {
		g.calls = make(map[string]*flightCall)
	}
	if existing, ok := g.calls[key]; ok {
		g.mu.Unlock()
		<-existing.done
		return existing.data, existing.format, existing.err
	}
	call := &flightCall{done: make(chan struct{})}
	g.calls[key] = call
	g.mu.Unlock()

	call.data, call.format, call.err = fn()
	close(call.done)

	g.mu.Lock()
	delete(g.calls, key)
	g.mu.Unlock()

	return call.data, call.format, call.err
}
