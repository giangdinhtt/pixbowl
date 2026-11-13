package store

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/giangdinhtt/pixbowl/internal/id"
)

// Local keeps objects under data/<shard>/<id>/... on the filesystem.
//
//	data/ab/ab3xK9mQ2pL7/meta.json
//	data/ab/ab3xK9mQ2pL7/image.webp
//	data/ab/ab3xK9mQ2pL7/original.jpg
//	data/ab/ab3xK9mQ2pL7/v/w320-h0-inside-webp
type Local struct {
	root string
}

func NewLocal(root string) *Local {
	return &Local{root: root}
}

func (l *Local) Init(_ context.Context) error {
	return os.MkdirAll(l.root, 0o750)
}

func (l *Local) path(imageID, rel string) (string, error) {
	if !id.Valid(imageID) {
		return "", ErrInvalidID
	}
	if !validRel(rel) {
		return "", ErrInvalidKey
	}
	return filepath.Join(l.root, imageID[:2], imageID, filepath.FromSlash(rel)), nil
}

func (l *Local) dir(imageID string) (string, error) {
	if !id.Valid(imageID) {
		return "", ErrInvalidID
	}
	return filepath.Join(l.root, imageID[:2], imageID), nil
}

func (l *Local) Put(_ context.Context, imageID, rel string, data []byte) error {
	target, err := l.path(imageID, rel)
	if err != nil {
		return err
	}
	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	// Write then rename so a reader never sees a half-written object.
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o640); err != nil {
		return err
	}
	return os.Rename(tmpName, target)
}

func (l *Local) PutMeta(ctx context.Context, meta *Meta) error {
	data, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	// meta.json is written last: until it exists the image is invisible.
	return l.Put(ctx, meta.ID, "meta.json", data)
}

func (l *Local) Meta(ctx context.Context, imageID string) (*Meta, error) {
	path, err := l.path(imageID, "meta.json")
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return decodeMeta(data)
}

func (l *Local) Open(_ context.Context, imageID, rel string) (io.ReadSeekCloser, int64, error) {
	path, err := l.path(imageID, rel)
	if err != nil {
		return nil, 0, err
	}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0, ErrNotFound
	}
	if err != nil {
		return nil, 0, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, 0, err
	}
	return file, info.Size(), nil
}

func (l *Local) Delete(_ context.Context, imageID string) error {
	dir, err := l.dir(imageID)
	if err != nil {
		return err
	}
	return os.RemoveAll(dir)
}

func (l *Local) Healthy(_ context.Context) error {
	info, err := os.Stat(l.root)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("store: data dir is not a directory")
	}
	return nil
}
