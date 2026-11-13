package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"time"

	"github.com/giangdinhtt/pixbowl/internal/config"
	"github.com/giangdinhtt/pixbowl/internal/id"
)

var (
	ErrNotFound   = errors.New("store: not found")
	ErrInvalidID  = errors.New("store: invalid id")
	ErrInvalidKey = errors.New("store: invalid object key")
)

// Rendition describes one stored copy of an image.
type Rendition struct {
	Name   string `json:"name"`
	Mime   string `json:"mime"`
	Size   int64  `json:"size"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	SHA256 string `json:"sha256"`
}

// Meta is the whole database. It lives next to the image as meta.json, or as an
// object in S3, which is why pixbowl needs no database server.
type Meta struct {
	ID              string     `json:"id"`
	CreatedAt       time.Time  `json:"created_at"`
	DeleteTokenHash string     `json:"delete_token_hash"`
	Filename        string     `json:"filename,omitempty"`
	Animated        bool       `json:"animated"`
	SourceFormat    string     `json:"source_format"`
	Image           Rendition  `json:"image"`
	Original        *Rendition `json:"original,omitempty"`
}

// Store is the only thing the HTTP layer knows about persistence.
type Store interface {
	Init(ctx context.Context) error
	Put(ctx context.Context, imageID, rel string, data []byte) error
	PutMeta(ctx context.Context, meta *Meta) error
	Meta(ctx context.Context, imageID string) (*Meta, error)
	Open(ctx context.Context, imageID, rel string) (io.ReadSeekCloser, int64, error)
	Delete(ctx context.Context, imageID string) error
	Healthy(ctx context.Context) error
}

// relPattern whitelists every object name the server can write or read. It is
// the second line of defence (after id.Valid) against path traversal.
var relPattern = regexp.MustCompile(`^(meta\.json|image\.[a-z0-9]{2,5}|original\.[a-z0-9]{2,5}|v/[a-z0-9-]{1,80})$`)

func validRel(rel string) bool {
	return relPattern.MatchString(rel)
}

// New returns the storage backend selected by configuration.
func New(ctx context.Context, cfg *config.Config) (Store, error) {
	if cfg.Storage == "s3" {
		return newS3(ctx, cfg.S3)
	}
	return NewLocal(cfg.DataDir), nil
}

func decodeMeta(data []byte) (*Meta, error) {
	var meta Meta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("store: corrupt meta: %w", err)
	}
	if !id.Valid(meta.ID) {
		return nil, fmt.Errorf("store: meta has invalid id")
	}
	return &meta, nil
}
