package config

import (
	"encoding/base64"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func loadMap(env map[string]string) (*Config, error) {
	return load(func(key string) (string, bool) {
		value, ok := env[key]
		return value, ok
	})
}

func TestDefaults(t *testing.T) {
	cfg, err := loadMap(map[string]string{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	checks := []struct {
		name string
		got  any
		want any
	}{
		{"port", cfg.Port, 8080},
		{"storage", cfg.Storage, "local"},
		{"output_format", cfg.OutputFormat, "preserve"},
		{"keep_original", cfg.KeepOriginal, true},
		{"strip_metadata", cfg.StripMetadata, true},
		{"quality", cfg.Quality, 82},
		{"max_dimension", cfg.MaxDimension, 2560},
		{"max_file_bytes", cfg.MaxFileBytes, int64(10 << 20)},
		{"max_files", cfg.MaxFiles, 10},
		{"rate_minute", cfg.RateLimit.Minute, 20},
		{"trust_proxy", cfg.TrustProxy, false},
	}
	for _, check := range checks {
		if check.got != check.want {
			t.Errorf("%s = %v, want %v", check.name, check.got, check.want)
		}
	}
	if len(cfg.AllowedSizes) != 10 {
		t.Fatalf("default AllowedSizes has %d entries", len(cfg.AllowedSizes))
	}
	if !cfg.SizeAllowed(320) || cfg.SizeAllowed(321) {
		t.Fatal("default size whitelist is wrong")
	}
}

func TestInvalidValues(t *testing.T) {
	cases := map[string]map[string]string{
		"output format": {"OUTPUT_FORMAT": "bmp"},
		"storage":       {"STORAGE": "ftp"},
		"quality":       {"QUALITY": "200"},
		"port":          {"PORT": "abc"},
		"max file":      {"MAX_FILE_MB": "0"},
		"allowed sizes": {"ALLOWED_SIZES": "320,nope"},
		"bool":          {"KEEP_ORIGINAL": "maybe"},
	}
	for name, env := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := loadMap(env); err == nil {
				t.Fatalf("expected error for %v", env)
			}
		})
	}
}

func TestS3RequiresCredentials(t *testing.T) {
	_, err := loadMap(map[string]string{"STORAGE": "s3"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "S3_ENDPOINT_URL") {
		t.Fatalf("error should list missing variables, got %v", err)
	}

	cfg, err := loadMap(map[string]string{
		"STORAGE":              "s3",
		"S3_ENDPOINT_URL":      "https://minio.example.com",
		"S3_ACCESS_KEY_ID":     "key",
		"S3_SECRET_ACCESS_KEY": "secret",
		"S3_BUCKET_NAME":       "bucket",
	})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.S3.Prefix != "images/" {
		t.Fatalf("prefix = %q", cfg.S3.Prefix)
	}
}

func TestAllowedSizesCanBeDisabled(t *testing.T) {
	cfg, err := loadMap(map[string]string{"ALLOWED_SIZES": ""})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.AllowedSizes) != 0 {
		t.Fatalf("expected empty whitelist, got %v", cfg.AllowedSizes)
	}
	if !cfg.SizeAllowed(999) {
		t.Fatal("empty whitelist must allow any size")
	}
}

func TestAllowedOriginsParsing(t *testing.T) {
	cfg, err := loadMap(map[string]string{"ALLOWED_ORIGINS": " https://a.com , https://b.com "})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.AllowedOrigins) != 2 || cfg.AllowedOrigins[0] != "https://a.com" {
		t.Fatalf("origins = %v", cfg.AllowedOrigins)
	}
}

func TestBasicAuthConfig(t *testing.T) {
	t.Run("plain", func(t *testing.T) {
		cfg, err := loadMap(map[string]string{"BASIC_AUTH": "alice: p@ss word"})
		if err != nil {
			t.Fatal(err)
		}
		if cfg.BasicAuthUser != "alice" || cfg.BasicAuthSecret != " p@ss word" {
			t.Fatalf("user=%q secret=%q", cfg.BasicAuthUser, cfg.BasicAuthSecret)
		}
		if cfg.BasicAuthHash != "" {
			t.Fatal("hash should be empty")
		}
		if !cfg.BasicAuthEnabled() {
			t.Fatal("basic auth should be enabled")
		}
	})

	t.Run("base64", func(t *testing.T) {
		encoded := base64.StdEncoding.EncodeToString([]byte("alice:s3cret"))
		cfg, err := loadMap(map[string]string{"BASIC_AUTH": encoded})
		if err != nil {
			t.Fatal(err)
		}
		if cfg.BasicAuthUser != "alice" || cfg.BasicAuthSecret != "s3cret" {
			t.Fatalf("user=%q secret=%q", cfg.BasicAuthUser, cfg.BasicAuthSecret)
		}
	})

	t.Run("base64 without padding", func(t *testing.T) {
		encoded := base64.RawStdEncoding.EncodeToString([]byte("bob:hunter2"))
		cfg, err := loadMap(map[string]string{"BASIC_AUTH": encoded})
		if err != nil {
			t.Fatal(err)
		}
		if cfg.BasicAuthUser != "bob" || cfg.BasicAuthSecret != "hunter2" {
			t.Fatalf("user=%q secret=%q", cfg.BasicAuthUser, cfg.BasicAuthSecret)
		}
	})

	t.Run("bcrypt", func(t *testing.T) {
		hash, err := bcrypt.GenerateFromPassword([]byte("s3cret"), bcrypt.MinCost)
		if err != nil {
			t.Fatal(err)
		}
		cfg, err := loadMap(map[string]string{"BASIC_AUTH": "alice:" + string(hash)})
		if err != nil {
			t.Fatal(err)
		}
		if cfg.BasicAuthHash == "" || cfg.BasicAuthSecret != "" {
			t.Fatalf("hash=%q secret=%q", cfg.BasicAuthHash, cfg.BasicAuthSecret)
		}
		if !cfg.BasicAuthEnabled() {
			t.Fatal("basic auth should be enabled")
		}
	})

	t.Run("rejects malformed", func(t *testing.T) {
		cases := []string{
			"alice",            // no colon and not base64 of user:pass
			"alice:",           // empty password
			":s3cret",          // empty user
			"alice:$2y$10$bad", // truncated bcrypt hash
		}
		for _, raw := range cases {
			if _, err := loadMap(map[string]string{"BASIC_AUTH": raw}); err == nil {
				t.Fatalf("%q should be rejected", raw)
			}
		}
	})

	t.Run("disabled by default", func(t *testing.T) {
		cfg, err := loadMap(map[string]string{})
		if err != nil {
			t.Fatal(err)
		}
		if cfg.BasicAuthEnabled() {
			t.Fatal("basic auth must be disabled when unset")
		}
	})
}
