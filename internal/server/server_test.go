package server

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	_ "golang.org/x/image/webp"

	"github.com/giangdinhtt/pixbowl/internal/config"
	"github.com/giangdinhtt/pixbowl/internal/imageproc"
	"github.com/giangdinhtt/pixbowl/internal/store"
)

func TestMain(m *testing.M) {
	if err := imageproc.Start(imageproc.RuntimeConfig{Concurrency: 1}); err != nil {
		panic(err)
	}
	code := m.Run()
	imageproc.Stop()
	os.Exit(code)
}

func testConfig(t *testing.T) *config.Config {
	t.Helper()
	return &config.Config{
		Host:                  "127.0.0.1",
		Storage:               "local",
		DataDir:               t.TempDir(),
		KeepOriginal:          true,
		OutputFormat:          "webp",
		Quality:               80,
		MaxDimension:          2560,
		StripMetadata:         true,
		MaxFileBytes:          2 << 20,
		MaxFiles:              5,
		MaxPixels:             40_000_000,
		RateLimit:             config.RateLimits{Minute: 100, Hour: 1000, Day: 10000},
		AuthAttemptsPerMinute: 10,
		Concurrency:           1,
		MaxConcurrentUploads:  2,
		UploadQueueLimit:      8,
		IDLength:              12,
		AllowedSizes:          []int{100, 200, 320, 640},
		MaxVariantDimension:   4096,
		MaxConcurrentVariants: 1,
		AdminToken:            "admin-secret",
		ShutdownTimeout:       time.Second,
	}
}

func newTestServer(t *testing.T, cfg *config.Config) *httptest.Server {
	t.Helper()
	if cfg == nil {
		cfg = testConfig(t)
	}
	st := store.NewLocal(cfg.DataDir)
	if err := st.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	srv := New(cfg, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		ts.Close()
		srv.Close()
	})
	return ts
}

type uploadErrorPayload struct {
	Filename string `json:"filename"`
	Error    string `json:"error"`
	Message  string `json:"message"`
}

type uploadPayload struct {
	ID          string               `json:"id"`
	URL         string               `json:"url"`
	OriginalURL string               `json:"original_url"`
	DeleteURL   string               `json:"delete_url"`
	DeleteToken string               `json:"delete_token"`
	Width       int                  `json:"width"`
	Height      int                  `json:"height"`
	Format      string               `json:"format"`
	Animated    bool                 `json:"animated"`
	Files       []uploadPayload      `json:"files"`
	Errors      []uploadErrorPayload `json:"errors"`
}

func testPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x % 256), uint8(y % 256), 90, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func uploadRequest(t *testing.T, ts *httptest.Server, files map[string][]byte) *http.Response {
	t.Helper()
	return uploadRequestAs(t, ts, files, "", "")
}

func uploadRequestAs(t *testing.T, ts *httptest.Server, files map[string][]byte, user, pass string) *http.Response {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for name, data := range files {
		part, err := writer.CreateFormFile("file", name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/upload", &body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if user != "" || pass != "" {
		req.SetBasicAuth(user, pass)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func decodeUpload(t *testing.T, resp *http.Response) uploadPayload {
	t.Helper()
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var payload uploadPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("decode %q: %v", raw, err)
	}
	return payload
}

func getWithAuth(t *testing.T, url, user, pass string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.SetBasicAuth(user, pass)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func get(t *testing.T, url string) *http.Response {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestUploadServeAndDelete(t *testing.T) {
	ts := newTestServer(t, nil)

	resp := uploadRequest(t, ts, map[string][]byte{"photo.png": testPNG(t, 800, 600)})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload status = %d", resp.StatusCode)
	}
	payload := decodeUpload(t, resp)
	if payload.ID == "" || payload.DeleteToken == "" {
		t.Fatalf("bad payload: %+v", payload)
	}
	if payload.Format != "webp" || payload.Width != 800 || payload.Height != 600 {
		t.Fatalf("unexpected metadata: %+v", payload)
	}

	imageResp := get(t, payload.URL)
	defer imageResp.Body.Close()
	if imageResp.StatusCode != http.StatusOK {
		t.Fatalf("GET image = %d", imageResp.StatusCode)
	}
	headers := imageResp.Header
	for header, want := range map[string]string{
		"Content-Type":                 "image/webp",
		"X-Content-Type-Options":       "nosniff",
		"Cross-Origin-Resource-Policy": "cross-origin",
	} {
		if got := headers.Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
	if !strings.Contains(headers.Get("Cache-Control"), "immutable") {
		t.Errorf("Cache-Control = %q", headers.Get("Cache-Control"))
	}
	if !strings.Contains(headers.Get("X-Robots-Tag"), "noindex") {
		t.Errorf("X-Robots-Tag = %q", headers.Get("X-Robots-Tag"))
	}

	entityTag := headers.Get("ETag")
	if entityTag == "" {
		t.Fatal("missing ETag")
	}
	conditional, err := http.NewRequest(http.MethodGet, payload.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	conditional.Header.Set("If-None-Match", entityTag)
	conditionalResp, err := http.DefaultClient.Do(conditional)
	if err != nil {
		t.Fatal(err)
	}
	defer conditionalResp.Body.Close()
	if conditionalResp.StatusCode != http.StatusNotModified {
		t.Fatalf("conditional GET = %d, want 304", conditionalResp.StatusCode)
	}

	original := get(t, payload.OriginalURL)
	defer original.Body.Close()
	if original.StatusCode != http.StatusOK || original.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("original = %d %q", original.StatusCode, original.Header.Get("Content-Type"))
	}

	// Wrong token must not delete anything.
	badDelete, err := http.NewRequest(http.MethodDelete, payload.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	badDelete.Header.Set("X-Delete-Token", "wrong")
	badResp, err := http.DefaultClient.Do(badDelete)
	if err != nil {
		t.Fatal(err)
	}
	badResp.Body.Close()
	if badResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad delete = %d, want 401", badResp.StatusCode)
	}

	goodDelete, err := http.NewRequest(http.MethodDelete, payload.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	goodDelete.Header.Set("X-Delete-Token", payload.DeleteToken)
	goodResp, err := http.DefaultClient.Do(goodDelete)
	if err != nil {
		t.Fatal(err)
	}
	goodResp.Body.Close()
	if goodResp.StatusCode != http.StatusOK {
		t.Fatalf("delete = %d, want 200", goodResp.StatusCode)
	}

	after := get(t, payload.URL)
	after.Body.Close()
	if after.StatusCode != http.StatusNotFound {
		t.Fatalf("after delete = %d, want 404", after.StatusCode)
	}
}

func TestVariantResize(t *testing.T) {
	ts := newTestServer(t, nil)
	payload := decodeUpload(t, uploadRequest(t, ts, map[string][]byte{"a.png": testPNG(t, 800, 600)}))

	resp := get(t, payload.URL+"?w=320")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("variant = %d", resp.StatusCode)
	}
	if resp.Header.Get("Content-Type") != "image/webp" {
		t.Fatalf("variant content type = %q", resp.Header.Get("Content-Type"))
	}
	cfg, _, err := image.DecodeConfig(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Width != 320 || cfg.Height != 240 {
		t.Fatalf("variant size = %dx%d", cfg.Width, cfg.Height)
	}

	cover := get(t, payload.URL+"?w=640&h=640&fit=cover")
	defer cover.Body.Close()
	coverCfg, _, err := image.DecodeConfig(cover.Body)
	if err != nil {
		t.Fatal(err)
	}
	if coverCfg.Width != 640 || coverCfg.Height != 640 {
		t.Fatalf("cover size = %dx%d", coverCfg.Width, coverCfg.Height)
	}

	invalid := get(t, payload.URL+"?w=333")
	invalid.Body.Close()
	if invalid.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid size = %d, want 400", invalid.StatusCode)
	}

	if !imageproc.SupportsFormat("avif") {
		avif := get(t, payload.URL+"?format=avif")
		avif.Body.Close()
		if avif.StatusCode != http.StatusBadRequest {
			t.Fatalf("unsupported format = %d, want 400", avif.StatusCode)
		}
	}
}

func TestRateLimit(t *testing.T) {
	cfg := testConfig(t)
	cfg.RateLimit.Minute = 1
	ts := newTestServer(t, cfg)

	first := uploadRequest(t, ts, map[string][]byte{"a.png": testPNG(t, 40, 30)})
	first.Body.Close()
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first upload = %d", first.StatusCode)
	}

	second := uploadRequest(t, ts, map[string][]byte{"b.png": testPNG(t, 40, 30)})
	defer second.Body.Close()
	if second.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("second upload = %d, want 429", second.StatusCode)
	}
	if second.Header.Get("Retry-After") == "" {
		t.Fatal("missing Retry-After")
	}
}

func TestCORS(t *testing.T) {
	ts := newTestServer(t, nil)

	preflight, err := http.NewRequest(http.MethodOptions, ts.URL+"/upload", nil)
	if err != nil {
		t.Fatal(err)
	}
	preflight.Header.Set("Origin", "https://evil.example")
	preflight.Header.Set("Access-Control-Request-Method", "POST")
	resp, err := http.DefaultClient.Do(preflight)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("denied preflight = %d, want 403", resp.StatusCode)
	}
	if resp.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("denied origin must not receive CORS headers")
	}

	cfg := testConfig(t)
	cfg.AllowedOrigins = []string{"https://app.example.com"}
	allowedServer := newTestServer(t, cfg)

	allowed, err := http.NewRequest(http.MethodOptions, allowedServer.URL+"/upload", nil)
	if err != nil {
		t.Fatal(err)
	}
	allowed.Header.Set("Origin", "https://app.example.com")
	allowed.Header.Set("Access-Control-Request-Method", "POST")
	allowedResp, err := http.DefaultClient.Do(allowed)
	if err != nil {
		t.Fatal(err)
	}
	allowedResp.Body.Close()
	if allowedResp.StatusCode != http.StatusNoContent {
		t.Fatalf("allowed preflight = %d, want 204", allowedResp.StatusCode)
	}
	if allowedResp.Header.Get("Access-Control-Allow-Origin") != "https://app.example.com" {
		t.Fatalf("ACAO = %q", allowedResp.Header.Get("Access-Control-Allow-Origin"))
	}
}

func TestRejectsNonImages(t *testing.T) {
	ts := newTestServer(t, nil)
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"></svg>`)

	resp := uploadRequest(t, ts, map[string][]byte{"evil.svg": svg})
	payload := decodeUpload(t, resp)
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("svg accepted: %+v", payload)
	}
	if len(payload.Errors) == 0 || payload.Errors[0].Error != "unsupported_image" {
		t.Fatalf("errors = %+v", payload.Errors)
	}
}

func TestMultipleFiles(t *testing.T) {
	ts := newTestServer(t, nil)
	resp := uploadRequest(t, ts, map[string][]byte{
		"a.png": testPNG(t, 200, 100),
		"b.png": testPNG(t, 300, 150),
	})
	payload := decodeUpload(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if len(payload.Files) != 2 {
		t.Fatalf("files = %d, want 2", len(payload.Files))
	}
}

func TestPagesAndHealth(t *testing.T) {
	ts := newTestServer(t, nil)

	index := get(t, ts.URL+"/")
	defer index.Body.Close()
	body, _ := io.ReadAll(index.Body)
	if index.StatusCode != http.StatusOK || !strings.Contains(string(body), "pixbowl") {
		t.Fatalf("index = %d", index.StatusCode)
	}
	if !strings.Contains(index.Header.Get("Content-Security-Policy"), "default-src 'none'") {
		t.Fatalf("missing CSP: %q", index.Header.Get("Content-Security-Policy"))
	}

	health := get(t, ts.URL+"/healthz")
	defer health.Body.Close()
	if health.StatusCode != http.StatusOK {
		t.Fatalf("health = %d", health.StatusCode)
	}

	robots := get(t, ts.URL+"/robots.txt")
	robots.Body.Close()
	if robots.StatusCode != http.StatusOK {
		t.Fatalf("robots = %d", robots.StatusCode)
	}
}

func TestTraversalIsRejected(t *testing.T) {
	ts := newTestServer(t, nil)
	for _, path := range []string{"/i/..%2F..%2Fetc%2Fpasswd", "/i/notanid.webp", "/o/zzzzzzzzzzzzzzzz"} {
		resp := get(t, ts.URL+path)
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s = %d, want 404", path, resp.StatusCode)
		}
	}
}

func TestAdminTokenCanDelete(t *testing.T) {
	ts := newTestServer(t, nil)
	payload := decodeUpload(t, uploadRequest(t, ts, map[string][]byte{"a.png": testPNG(t, 40, 40)}))

	req, err := http.NewRequest(http.MethodDelete, payload.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer admin-secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("admin delete = %d", resp.StatusCode)
	}
}

func TestBasicAuthGuardsUploadOnly(t *testing.T) {
	cfg := testConfig(t)
	cfg.BasicAuthUser = "alice"
	cfg.BasicAuthPass = "s3cret pass"
	ts := newTestServer(t, cfg)

	anonymous := get(t, ts.URL+"/")
	anonymous.Body.Close()
	if anonymous.StatusCode != http.StatusUnauthorized {
		t.Fatalf("index without credentials = %d, want 401", anonymous.StatusCode)
	}
	if !strings.HasPrefix(anonymous.Header.Get("WWW-Authenticate"), "Basic ") {
		t.Fatalf("WWW-Authenticate = %q", anonymous.Header.Get("WWW-Authenticate"))
	}

	wrong := getWithAuth(t, ts.URL+"/", "alice", "wrong")
	wrong.Body.Close()
	if wrong.StatusCode != http.StatusUnauthorized {
		t.Fatalf("index with wrong password = %d, want 401", wrong.StatusCode)
	}

	authorised := getWithAuth(t, ts.URL+"/", "alice", "s3cret pass")
	authorised.Body.Close()
	if authorised.StatusCode != http.StatusOK {
		t.Fatalf("index with credentials = %d, want 200", authorised.StatusCode)
	}

	denied := uploadRequestAs(t, ts, map[string][]byte{"a.png": testPNG(t, 40, 40)}, "", "")
	denied.Body.Close()
	if denied.StatusCode != http.StatusUnauthorized {
		t.Fatalf("upload without credentials = %d, want 401", denied.StatusCode)
	}

	allowed := uploadRequestAs(t, ts, map[string][]byte{"a.png": testPNG(t, 40, 40)}, "alice", "s3cret pass")
	if allowed.StatusCode != http.StatusOK {
		t.Fatalf("upload with credentials = %d, want 200", allowed.StatusCode)
	}
	payload := decodeUpload(t, allowed)

	published := get(t, payload.URL)
	published.Body.Close()
	if published.StatusCode != http.StatusOK {
		t.Fatalf("public image link = %d, want 200", published.StatusCode)
	}

	deletePage := get(t, ts.URL+"/d/"+payload.ID)
	deletePage.Body.Close()
	if deletePage.StatusCode != http.StatusOK {
		t.Fatalf("delete page = %d, want 200", deletePage.StatusCode)
	}

	health := get(t, ts.URL+"/healthz")
	health.Body.Close()
	if health.StatusCode != http.StatusOK {
		t.Fatalf("healthz = %d, want 200", health.StatusCode)
	}
}
