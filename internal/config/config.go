package config

import (
	"encoding/base64"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type lookupFunc func(string) (string, bool)

// RateLimits holds the three per-IP upload windows. Zero disables a window.
type RateLimits struct {
	Minute int
	Hour   int
	Day    int
}

type S3Config struct {
	Endpoint  string
	Region    string
	AccessKey string
	SecretKey string
	Bucket    string
	Prefix    string
	UseSSL    bool
}

// Config is the fully resolved runtime configuration.
type Config struct {
	Port       int
	Host       string
	BaseURL    string
	DataDir    string
	LogLevel   string
	TrustProxy bool

	Storage       string // local | s3
	KeepOriginal  bool
	OutputFormat  string // preserve | jpeg | png | webp | avif
	Quality       int
	MaxDimension  int
	StripMetadata bool
	MaxFileBytes  int64
	MaxFiles      int
	MaxPixels     int

	RateLimit             RateLimits
	AuthAttemptsPerMinute int
	AllowedOrigins        []string
	AdminToken            string
	BasicAuthUser         string
	BasicAuthSecret       string // plain-text password, empty when BasicAuthHash is used
	BasicAuthHash         string // bcrypt hash, mutually exclusive with BasicAuthSecret

	Concurrency           int
	MaxConcurrentUploads  int
	UploadQueueLimit      int
	IDLength              int
	AllowedSizes          []int
	MaxVariantDimension   int
	MaxConcurrentVariants int

	S3              S3Config
	ShutdownTimeout time.Duration
}

var validOutputFormats = map[string]bool{
	"preserve": true,
	"jpeg":     true,
	"png":      true,
	"webp":     true,
	"avif":     true,
}

var defaultSizes = []int{100, 200, 320, 480, 640, 800, 1200, 1600, 2000, 2560}

// Load builds the configuration from environment variables, applying defaults
// and rejecting invalid values early so the process never starts half-broken.
func Load() (*Config, error) { return load(os.LookupEnv) }

// load takes the environment lookup as a parameter so tests can inject a map.
func load(lookup lookupFunc) (*Config, error) {
	cfg := &Config{
		Port:                  8080,
		Host:                  "0.0.0.0",
		LogLevel:              "info",
		Storage:               "local",
		KeepOriginal:          true,
		OutputFormat:          "preserve",
		Quality:               82,
		MaxDimension:          2560,
		StripMetadata:         true,
		MaxFileBytes:          10 << 20,
		MaxFiles:              10,
		MaxPixels:             40_000_000,
		RateLimit:             RateLimits{Minute: 20, Hour: 200, Day: 1000},
		AuthAttemptsPerMinute: 10,
		Concurrency:           1,
		MaxConcurrentUploads:  2,
		UploadQueueLimit:      32,
		IDLength:              12,
		AllowedSizes:          defaultSizes,
		MaxVariantDimension:   4096,
		MaxConcurrentVariants: 1,
		ShutdownTimeout:       20 * time.Second,
	}

	var err error
	if cfg.Port, err = envInt(lookup, "PORT", cfg.Port); err != nil {
		return nil, err
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return nil, fmt.Errorf("PORT must be between 1 and 65535")
	}
	cfg.Host = envString(lookup, "HOST", cfg.Host)
	cfg.BaseURL = strings.TrimRight(envString(lookup, "BASE_URL", ""), "/")
	cfg.LogLevel = strings.ToLower(envString(lookup, "LOG_LEVEL", cfg.LogLevel))
	cfg.DataDir = envString(lookup, "DATA_DIR", "./data")

	if cfg.TrustProxy, err = envBool(lookup, "TRUST_PROXY", cfg.TrustProxy); err != nil {
		return nil, err
	}
	cfg.Storage = strings.ToLower(envString(lookup, "STORAGE", cfg.Storage))
	if cfg.Storage != "local" && cfg.Storage != "s3" {
		return nil, fmt.Errorf(`STORAGE must be "local" or "s3"`)
	}

	if cfg.KeepOriginal, err = envBool(lookup, "KEEP_ORIGINAL", cfg.KeepOriginal); err != nil {
		return nil, err
	}
	cfg.OutputFormat = strings.ToLower(envString(lookup, "OUTPUT_FORMAT", cfg.OutputFormat))
	if !validOutputFormats[cfg.OutputFormat] {
		return nil, fmt.Errorf("OUTPUT_FORMAT must be one of preserve, jpeg, png, webp, avif")
	}
	if cfg.Quality, err = envInt(lookup, "QUALITY", cfg.Quality); err != nil {
		return nil, err
	}
	if cfg.Quality < 1 || cfg.Quality > 100 {
		return nil, fmt.Errorf("QUALITY must be between 1 and 100")
	}
	if cfg.MaxDimension, err = envInt(lookup, "MAX_DIMENSION", cfg.MaxDimension); err != nil {
		return nil, err
	}
	if cfg.MaxDimension < 0 || cfg.MaxDimension > 20000 {
		return nil, fmt.Errorf("MAX_DIMENSION must be between 0 and 20000")
	}
	if cfg.StripMetadata, err = envBool(lookup, "STRIP_METADATA", cfg.StripMetadata); err != nil {
		return nil, err
	}

	maxFileMB, err := envInt(lookup, "MAX_FILE_MB", int(cfg.MaxFileBytes>>20))
	if err != nil {
		return nil, err
	}
	if maxFileMB < 1 || maxFileMB > 200 {
		return nil, fmt.Errorf("MAX_FILE_MB must be between 1 and 200")
	}
	cfg.MaxFileBytes = int64(maxFileMB) << 20

	if cfg.MaxFiles, err = envInt(lookup, "MAX_FILES_PER_UPLOAD", cfg.MaxFiles); err != nil {
		return nil, err
	}
	if cfg.MaxFiles < 1 || cfg.MaxFiles > 50 {
		return nil, fmt.Errorf("MAX_FILES_PER_UPLOAD must be between 1 and 50")
	}
	if cfg.MaxPixels, err = envInt(lookup, "MAX_PIXELS", cfg.MaxPixels); err != nil {
		return nil, err
	}
	if cfg.MaxPixels < 100_000 {
		return nil, fmt.Errorf("MAX_PIXELS must be at least 100000")
	}

	if cfg.RateLimit.Minute, err = envInt(lookup, "RATE_LIMIT_PER_MINUTE", cfg.RateLimit.Minute); err != nil {
		return nil, err
	}
	if cfg.RateLimit.Hour, err = envInt(lookup, "RATE_LIMIT_PER_HOUR", cfg.RateLimit.Hour); err != nil {
		return nil, err
	}
	if cfg.RateLimit.Day, err = envInt(lookup, "RATE_LIMIT_PER_DAY", cfg.RateLimit.Day); err != nil {
		return nil, err
	}
	for name, value := range map[string]int{
		"RATE_LIMIT_PER_MINUTE": cfg.RateLimit.Minute,
		"RATE_LIMIT_PER_HOUR":   cfg.RateLimit.Hour,
		"RATE_LIMIT_PER_DAY":    cfg.RateLimit.Day,
	} {
		if value < 0 {
			return nil, fmt.Errorf("%s must not be negative", name)
		}
	}
	if cfg.AuthAttemptsPerMinute, err = envInt(lookup, "AUTH_ATTEMPTS_PER_MINUTE", cfg.AuthAttemptsPerMinute); err != nil {
		return nil, err
	}
	if cfg.AuthAttemptsPerMinute < 0 {
		return nil, fmt.Errorf("AUTH_ATTEMPTS_PER_MINUTE must not be negative")
	}

	cfg.AllowedOrigins = splitList(envString(lookup, "ALLOWED_ORIGINS", ""))
	cfg.AdminToken = envString(lookup, "ADMIN_TOKEN", "")

	// Basic auth only guards the upload UI and the upload endpoint; public
	// image links stay reachable without credentials.
	if err := cfg.applyBasicAuth(envString(lookup, "BASIC_AUTH", "")); err != nil {
		return nil, err
	}

	if cfg.Concurrency, err = envInt(lookup, "VIPS_CONCURRENCY", cfg.Concurrency); err != nil {
		return nil, err
	}
	if cfg.Concurrency < 1 || cfg.Concurrency > 16 {
		return nil, fmt.Errorf("VIPS_CONCURRENCY must be between 1 and 16")
	}
	if cfg.MaxConcurrentUploads, err = envInt(lookup, "MAX_CONCURRENT_UPLOADS", cfg.MaxConcurrentUploads); err != nil {
		return nil, err
	}
	if cfg.MaxConcurrentUploads < 1 || cfg.MaxConcurrentUploads > 16 {
		return nil, fmt.Errorf("MAX_CONCURRENT_UPLOADS must be between 1 and 16")
	}
	if cfg.UploadQueueLimit, err = envInt(lookup, "UPLOAD_QUEUE_LIMIT", cfg.UploadQueueLimit); err != nil {
		return nil, err
	}
	if cfg.IDLength, err = envInt(lookup, "ID_LENGTH", cfg.IDLength); err != nil {
		return nil, err
	}
	if cfg.IDLength < 8 || cfg.IDLength > 64 {
		return nil, fmt.Errorf("ID_LENGTH must be between 8 and 64")
	}

	if raw, ok := lookup("ALLOWED_SIZES"); ok {
		cfg.AllowedSizes, err = parseSizes(raw)
		if err != nil {
			return nil, err
		}
	}
	if cfg.MaxVariantDimension, err = envInt(lookup, "MAX_VARIANT_DIMENSION", cfg.MaxVariantDimension); err != nil {
		return nil, err
	}
	if cfg.MaxVariantDimension < 16 || cfg.MaxVariantDimension > 20000 {
		return nil, fmt.Errorf("MAX_VARIANT_DIMENSION must be between 16 and 20000")
	}
	if cfg.MaxConcurrentVariants, err = envInt(lookup, "MAX_CONCURRENT_VARIANTS", cfg.MaxConcurrentVariants); err != nil {
		return nil, err
	}
	if cfg.MaxConcurrentVariants < 1 {
		return nil, fmt.Errorf("MAX_CONCURRENT_VARIANTS must be at least 1")
	}

	cfg.S3 = S3Config{
		Endpoint:  envString(lookup, "S3_ENDPOINT_URL", ""),
		Region:    envString(lookup, "S3_REGION", "us-east-1"),
		AccessKey: envString(lookup, "S3_ACCESS_KEY_ID", ""),
		SecretKey: envString(lookup, "S3_SECRET_ACCESS_KEY", ""),
		Bucket:    envString(lookup, "S3_BUCKET_NAME", ""),
		Prefix:    strings.TrimLeft(envString(lookup, "S3_PREFIX", "images/"), "/"),
	}
	if cfg.S3.UseSSL, err = envBool(lookup, "S3_USE_SSL", true); err != nil {
		return nil, err
	}
	if cfg.S3.Prefix == "" {
		cfg.S3.Prefix = "images/"
	}
	if !strings.HasSuffix(cfg.S3.Prefix, "/") {
		cfg.S3.Prefix += "/"
	}

	if cfg.Storage == "s3" {
		missing := []string{}
		for name, value := range map[string]string{
			"S3_ENDPOINT_URL":      cfg.S3.Endpoint,
			"S3_ACCESS_KEY_ID":     cfg.S3.AccessKey,
			"S3_SECRET_ACCESS_KEY": cfg.S3.SecretKey,
			"S3_BUCKET_NAME":       cfg.S3.Bucket,
		} {
			if value == "" {
				missing = append(missing, name)
			}
		}
		if len(missing) > 0 {
			return nil, fmt.Errorf("STORAGE=s3 requires: %s", strings.Join(missing, ", "))
		}
	}

	return cfg, nil
}

func envString(lookup lookupFunc, key, fallback string) string {
	if raw, ok := lookup(key); ok {
		value := strings.TrimSpace(raw)
		if value != "" {
			return value
		}
	}
	return fallback
}

func envInt(lookup lookupFunc, key string, fallback int) (int, error) {
	raw, ok := lookup(key)
	if !ok || strings.TrimSpace(raw) == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer, got %q", key, raw)
	}
	return value, nil
}

func envBool(lookup lookupFunc, key string, fallback bool) (bool, error) {
	raw, ok := lookup(key)
	if !ok || strings.TrimSpace(raw) == "" {
		return fallback, nil
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true, nil
	case "0", "false", "no", "off":
		return false, nil
	default:
		return false, fmt.Errorf("%s must be a boolean, got %q", key, raw)
	}
}

func splitList(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func parseSizes(raw string) ([]int, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil // empty means "any size in range"
	}
	sizes := []int{}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		value, err := strconv.Atoi(part)
		if err != nil || value < 1 || value > 20000 {
			return nil, fmt.Errorf("ALLOWED_SIZES contains an invalid size %q", part)
		}
		sizes = append(sizes, value)
	}
	return sizes, nil
}

// BasicAuthEnabled reports whether the upload UI is password protected.
func (c *Config) BasicAuthEnabled() bool {
	return c.BasicAuthUser != "" && (c.BasicAuthSecret != "" || c.BasicAuthHash != "")
}

// applyBasicAuth decodes the single BASIC_AUTH variable. Accepted forms:
//
//	alice:secret                      plain user:password
//	base64("alice:secret")            same value an Authorization header carries
//	alice:$2y$10$...                  bcrypt hash, so no plaintext secret is stored
func (c *Config) applyBasicAuth(raw string) error {
	user, secret, hash, err := parseBasicAuth(raw)
	if err != nil {
		return err
	}
	c.BasicAuthUser, c.BasicAuthSecret, c.BasicAuthHash = user, secret, hash
	return nil
}

func parseBasicAuth(raw string) (user, secret, hash string, err error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", "", "", nil
	}

	decoded := value
	if !strings.Contains(value, ":") {
		// Base64 never contains ':', so its presence means the value is already
		// in user:secret form. Try the padded and unpadded alphabets.
		decoded = ""
		for _, encoding := range []*base64.Encoding{
			base64.StdEncoding,
			base64.RawStdEncoding,
			base64.URLEncoding,
			base64.RawURLEncoding,
		} {
			if data, decodeErr := encoding.DecodeString(value); decodeErr == nil {
				decoded = string(data)
				break
			}
		}
		if decoded == "" {
			return "", "", "", fmt.Errorf(`BASIC_AUTH must be "user:password", base64("user:password") or "user:<bcrypt hash>"`)
		}
	}

	user, secret, found := strings.Cut(decoded, ":")
	if !found || user == "" || secret == "" {
		return "", "", "", fmt.Errorf(`BASIC_AUTH must contain "user:password"`)
	}
	if strings.HasPrefix(secret, "$2") {
		// Looks like a bcrypt hash; refuse to fall back to treating a typo as
		// a plain-text password.
		if _, costErr := bcrypt.Cost([]byte(secret)); costErr != nil {
			return "", "", "", fmt.Errorf("BASIC_AUTH contains an invalid bcrypt hash: %w", costErr)
		}
		return user, "", secret, nil
	}
	return user, secret, "", nil
}

// SizeAllowed reports whether a requested variant dimension is permitted.
// An empty AllowedSizes means every value in range is accepted.
func (c *Config) SizeAllowed(size int) bool {
	if len(c.AllowedSizes) == 0 {
		return true
	}
	for _, allowed := range c.AllowedSizes {
		if allowed == size {
			return true
		}
	}
	return false
}
