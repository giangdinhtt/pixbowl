package config

import (
	"strings"
	"testing"
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
	if _, err := loadMap(map[string]string{"BASIC_AUTH_USER": "alice"}); err == nil {
		t.Fatal("user without password must be rejected")
	}
	if _, err := loadMap(map[string]string{"BASIC_AUTH_PASS": "secret"}); err == nil {
		t.Fatal("password without user must be rejected")
	}

	cfg, err := loadMap(map[string]string{
		"BASIC_AUTH_USER": "  alice  ",
		"BASIC_AUTH_PASS": " p@ss word ",
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BasicAuthUser != "alice" {
		t.Fatalf("user = %q, want trimmed", cfg.BasicAuthUser)
	}
	if cfg.BasicAuthPass != " p@ss word " {
		t.Fatalf("password whitespace must be preserved, got %q", cfg.BasicAuthPass)
	}
	if !cfg.BasicAuthEnabled() {
		t.Fatal("basic auth should be enabled")
	}

	disabled, err := loadMap(map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if disabled.BasicAuthEnabled() {
		t.Fatal("basic auth must be disabled when unset")
	}
}
