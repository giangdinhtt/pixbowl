package store

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/giangdinhtt/pixbowl/internal/config"
)

// fakeS3 is a minimal path-style S3 endpoint: enough for minio-go to create a
// bucket, put/get/head/delete objects and list them. Requests are anonymous.
type fakeS3 struct {
	mu      sync.Mutex
	buckets map[string]bool
	objects map[string][]byte
}

func newFakeS3() *fakeS3 {
	return &fakeS3{buckets: map[string]bool{}, objects: map[string][]byte{}}
}

func s3Error(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?><Error><Code>%s</Code><Message>%s</Message><RequestId>1</RequestId></Error>`, code, code)
}

func (f *fakeS3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	path := strings.TrimPrefix(r.URL.Path, "/")
	bucket, key, _ := strings.Cut(path, "/")
	query := r.URL.Query()

	switch {
	case key == "" && r.Method == http.MethodHead:
		if !f.buckets[bucket] {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
		return

	case key == "" && r.Method == http.MethodPut:
		f.buckets[bucket] = true
		w.WriteHeader(http.StatusOK)
		return

	case key == "" && r.Method == http.MethodGet && query.Get("location") != "":
		w.Header().Set("Content-Type", "application/xml")
		io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?><LocationConstraint xmlns="http://s3.amazonaws.com/doc/2006-03-01/">us-east-1</LocationConstraint>`)
		return

	case key == "" && r.Method == http.MethodGet && (query.Get("list-type") == "2" || query.Has("prefix")):
		prefix := query.Get("prefix")
		var contents strings.Builder
		count := 0
		bucketPrefix := bucket + "/" + prefix
		for objectKey, data := range f.objects {
			if !strings.HasPrefix(objectKey, bucketPrefix) {
				continue
			}
			count++
			fmt.Fprintf(&contents, `<Contents><Key>%s</Key><Size>%d</Size><LastModified>2024-01-01T00:00:00.000Z</LastModified><ETag>"etag"</ETag><StorageClass>STANDARD</StorageClass></Contents>`,
				xmlEscape(strings.TrimPrefix(objectKey, bucket+"/")), len(data))
		}
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?><ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Name>%s</Name><Prefix>%s</Prefix><KeyCount>%d</KeyCount><MaxKeys>1000</MaxKeys><IsTruncated>false</IsTruncated>%s</ListBucketResult>`,
			bucket, xmlEscape(prefix), count, contents.String())
		return

	case key == "" && r.Method == http.MethodPost && query.Has("delete"):
		body, _ := io.ReadAll(r.Body)
		var request struct {
			Keys []string `xml:"Object>Key"`
		}
		_ = xml.Unmarshal(body, &request)
		var deleted strings.Builder
		for _, objectKey := range request.Keys {
			delete(f.objects, bucket+"/"+objectKey)
			fmt.Fprintf(&deleted, `<Deleted><Key>%s</Key></Deleted>`, xmlEscape(objectKey))
		}
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?><DeleteResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">%s</DeleteResult>`, deleted.String())
		return
	}

	fullKey := bucket + "/" + key
	data, exists := f.objects[fullKey]

	switch r.Method {
	case http.MethodPut:
		body, err := io.ReadAll(r.Body)
		if err != nil {
			s3Error(w, http.StatusBadRequest, "InvalidRequest")
			return
		}
		f.objects[fullKey] = body
		w.Header().Set("ETag", `"etag"`)
		w.WriteHeader(http.StatusOK)

	case http.MethodHead:
		if !exists {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Length", fmt.Sprint(len(data)))
		w.Header().Set("ETag", `"etag"`)
		w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
		w.WriteHeader(http.StatusOK)

	case http.MethodGet:
		if !exists {
			s3Error(w, http.StatusNotFound, "NoSuchKey")
			return
		}
		w.Header().Set("Content-Length", fmt.Sprint(len(data)))
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("ETag", `"etag"`)
		w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)

	case http.MethodDelete:
		delete(f.objects, fullKey)
		w.WriteHeader(http.StatusNoContent)

	default:
		s3Error(w, http.StatusMethodNotAllowed, "MethodNotAllowed")
	}
}

func xmlEscape(value string) string {
	var buf strings.Builder
	_ = xml.EscapeText(&buf, []byte(value))
	return buf.String()
}

func TestS3RoundTrip(t *testing.T) {
	fake := newFakeS3()
	ts := httptest.NewServer(fake)
	defer ts.Close()

	ctx := context.Background()
	backend, err := newS3(ctx, config.S3Config{
		Endpoint: ts.URL,
		Region:   "us-east-1",
		Bucket:   "pix",
		Prefix:   "images/",
		UseSSL:   false,
	})
	if err != nil {
		t.Fatalf("newS3: %v", err)
	}

	imageID := "aB3xK9mQ2pL7"
	meta := &Meta{
		ID:              imageID,
		DeleteTokenHash: "hash",
		SourceFormat:    "jpeg",
		Image:           Rendition{Name: "image.webp", Mime: "image/webp", Size: 4, SHA256: "sha"},
	}
	payload := []byte("RIFFdata")

	if err := backend.Put(ctx, imageID, meta.Image.Name, payload); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := backend.PutMeta(ctx, meta); err != nil {
		t.Fatalf("put meta: %v", err)
	}

	loaded, err := backend.Meta(ctx, imageID)
	if err != nil {
		t.Fatalf("meta: %v", err)
	}
	if loaded.ID != imageID || loaded.Image.Name != "image.webp" {
		t.Fatalf("unexpected meta: %+v", loaded)
	}

	file, size, err := backend.Open(ctx, imageID, "image.webp")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	data, err := io.ReadAll(file)
	file.Close()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != string(payload) || size != int64(len(payload)) {
		t.Fatalf("data = %q size = %d", data, size)
	}

	if err := backend.Delete(ctx, imageID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := backend.Meta(ctx, imageID); err != ErrNotFound {
		t.Fatalf("meta after delete = %v, want ErrNotFound", err)
	}
	if len(fake.objects) != 0 {
		t.Fatalf("objects left behind: %v", fake.objects)
	}
}

func TestS3KeyLayoutAndValidation(t *testing.T) {
	backend := &S3{prefix: "images/"}
	key, err := backend.key("aB3xK9mQ2pL7", "v/w320-h0-inside-webp")
	if err != nil {
		t.Fatal(err)
	}
	if key != "images/aB3xK9mQ2pL7/v/w320-h0-inside-webp" {
		t.Fatalf("key = %q", key)
	}
	if _, err := backend.key("aB3xK9mQ2pL7", "../../etc/passwd"); err == nil {
		t.Fatal("expected traversal to be rejected")
	}
	if _, err := backend.key("../bad", "image.webp"); err == nil {
		t.Fatal("expected bad id to be rejected")
	}
}

func TestParseEndpoint(t *testing.T) {
	cases := []struct {
		raw     string
		secure  bool
		want    string
		wantSSL bool
	}{
		{"https://minio.example.com", true, "minio.example.com", true},
		{"http://127.0.0.1:9000", true, "127.0.0.1:9000", false},
		{"s3.amazonaws.com", true, "s3.amazonaws.com", true},
		{"s3.amazonaws.com", false, "s3.amazonaws.com", false},
	}
	for _, c := range cases {
		got, ssl, err := parseEndpoint(c.raw, c.secure)
		if err != nil {
			t.Fatalf("%s: %v", c.raw, err)
		}
		if got != c.want || ssl != c.wantSSL {
			t.Fatalf("%s => %q %v", c.raw, got, ssl)
		}
	}
}
