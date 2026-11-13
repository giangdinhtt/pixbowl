package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/giangdinhtt/pixbowl/internal/imageproc"
	"github.com/giangdinhtt/pixbowl/internal/store"
)

type variantSpec struct {
	Width  int
	Height int
	Fit    string
	Format string
}

func (v variantSpec) key() string {
	return fmt.Sprintf("w%d-h%d-%s-%s", v.Width, v.Height, v.Fit, v.Format)
}

func (s *Server) handleImage(w http.ResponseWriter, r *http.Request) {
	imageID, ok := imageIDFromName(r.PathValue("name"))
	if !ok {
		s.notFound(w)
		return
	}
	meta, err := s.store.Meta(r.Context(), imageID)
	if err != nil {
		s.storeError(w, err)
		return
	}

	spec, hasVariant, variantErr := s.parseVariant(r.URL.Query(), meta)
	if variantErr != nil {
		s.writeError(w, http.StatusBadRequest, "invalid_resize", variantErr.Error())
		return
	}
	if !hasVariant {
		s.serveRendition(w, r, meta, meta.Image)
		return
	}
	s.serveVariant(w, r, meta, spec)
}

func (s *Server) handleOriginal(w http.ResponseWriter, r *http.Request) {
	imageID, ok := imageIDFromName(r.PathValue("name"))
	if !ok {
		s.notFound(w)
		return
	}
	meta, err := s.store.Meta(r.Context(), imageID)
	if err != nil {
		s.storeError(w, err)
		return
	}
	if meta.Original == nil {
		s.writeError(w, http.StatusNotFound, "original_not_kept", "No original is stored for this image (KEEP_ORIGINAL=false, or it is identical to the optimised copy).")
		return
	}
	s.serveRendition(w, r, meta, *meta.Original)
}

func (s *Server) serveRendition(w http.ResponseWriter, r *http.Request, meta *store.Meta, rendition store.Rendition) {
	file, _, err := s.store.Open(r.Context(), meta.ID, rendition.Name)
	if err != nil {
		s.storeError(w, err)
		return
	}
	defer file.Close()

	s.writeImageHeaders(w, rendition.Mime, etag(rendition.SHA256, ""))
	http.ServeContent(w, r, "", time.Time{}, file)
}

func (s *Server) serveVariant(w http.ResponseWriter, r *http.Request, meta *store.Meta, spec variantSpec) {
	key := spec.key()
	variantETag := etag(meta.Image.SHA256, key)

	if file, _, err := s.store.Open(r.Context(), meta.ID, "v/"+key); err == nil {
		defer file.Close()
		s.writeImageHeaders(w, imageproc.Mime(spec.Format), variantETag)
		http.ServeContent(w, r, "", time.Time{}, file)
		return
	} else if !errors.Is(err, store.ErrNotFound) {
		s.log.Warn("variant.open_failed", "id", meta.ID, "key", key, "err", err)
	}

	data, format, err := s.inflight.Do(meta.ID+"/"+key, func() ([]byte, string, error) {
		return s.generateVariant(r.Context(), meta, spec, key)
	})
	if err != nil {
		var imageErr *imageproc.ImageError
		if errors.As(err, &imageErr) {
			s.writeError(w, http.StatusUnprocessableEntity, "resize_failed", imageErr.Msg)
			return
		}
		s.log.Error("variant.failed", "id", meta.ID, "key", key, "err", err)
		s.writeError(w, http.StatusBadGateway, "resize_failed", "Could not generate the resized variant.")
		return
	}

	s.writeImageHeaders(w, imageproc.Mime(format), variantETag)
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(data))
}

func (s *Server) generateVariant(ctx context.Context, meta *store.Meta, spec variantSpec, key string) ([]byte, string, error) {
	// A sibling request (or another process) may have produced it already.
	if file, _, err := s.store.Open(ctx, meta.ID, "v/"+key); err == nil {
		defer file.Close()
		if data, err := io.ReadAll(io.LimitReader(file, s.cfg.MaxFileBytes+(1<<20))); err == nil {
			return data, spec.Format, nil
		}
	}

	select {
	case s.variantSem <- struct{}{}:
	case <-ctx.Done():
		return nil, "", ctx.Err()
	}
	defer func() { <-s.variantSem }()

	source := meta.Image
	if meta.Original != nil {
		source = *meta.Original
	}

	file, _, err := s.store.Open(ctx, meta.ID, source.Name)
	if err != nil {
		return nil, "", err
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, s.cfg.MaxFileBytes+(1<<20)))
	if err != nil {
		return nil, "", err
	}

	out, err := imageproc.Process(data, imageproc.Options{
		Format:        spec.Format,
		Width:         spec.Width,
		Height:        spec.Height,
		Fit:           spec.Fit,
		Quality:       s.cfg.Quality,
		StripMetadata: true,
		MaxPixels:     s.cfg.MaxPixels,
	})
	if err != nil {
		return nil, "", err
	}

	if err := s.store.Put(ctx, meta.ID, "v/"+key, out.Data); err != nil {
		// Serving the freshly rendered bytes still succeeds; caching is best effort.
		s.log.Warn("variant.persist_failed", "id", meta.ID, "key", key, "err", err)
	}
	s.log.Debug("variant.generated", "id", meta.ID, "key", key, "bytes", len(out.Data))
	return out.Data, out.Format, nil
}

func (s *Server) parseVariant(query url.Values, meta *store.Meta) (variantSpec, bool, error) {
	// Animated images are served untouched: re-encoding would drop frames.
	if meta.Animated {
		return variantSpec{}, false, nil
	}

	widthRaw := strings.TrimSpace(query.Get("w"))
	heightRaw := strings.TrimSpace(query.Get("h"))
	fitRaw := strings.ToLower(strings.TrimSpace(query.Get("fit")))
	formatRaw := strings.ToLower(strings.TrimSpace(query.Get("format")))

	if widthRaw == "" && heightRaw == "" && fitRaw == "" && formatRaw == "" {
		return variantSpec{}, false, nil
	}

	spec := variantSpec{Fit: "inside"}
	if widthRaw != "" {
		width, err := s.parseSize(widthRaw, "w")
		if err != nil {
			return spec, false, err
		}
		spec.Width = width
	}
	if heightRaw != "" {
		height, err := s.parseSize(heightRaw, "h")
		if err != nil {
			return spec, false, err
		}
		spec.Height = height
	}
	if fitRaw != "" {
		switch fitRaw {
		case "inside", "cover", "contain":
			spec.Fit = fitRaw
		default:
			return spec, false, fmt.Errorf("fit must be inside, cover or contain")
		}
	}

	baseFormat := formatFromName(meta.Image.Name)
	if formatRaw == "" {
		formatRaw = baseFormat
	}
	if !imageproc.SupportsFormat(formatRaw) {
		return spec, false, fmt.Errorf("this server cannot encode %q", formatRaw)
	}
	spec.Format = formatRaw

	// Never upscale, except for cover which promises an exact box. The image
	// pipeline applies its own 2x ceiling on top of this.
	if !(spec.Fit == "cover" && spec.Width > 0 && spec.Height > 0) {
		if spec.Width > meta.Image.Width {
			spec.Width = meta.Image.Width
		}
		if spec.Height > meta.Image.Height {
			spec.Height = meta.Image.Height
		}
	}

	// Asking for the same bytes we already have is a no-op, not a cache entry.
	if spec.Format == baseFormat && spec.Fit == "inside" &&
		(spec.Width == 0 || spec.Width >= meta.Image.Width) &&
		(spec.Height == 0 || spec.Height >= meta.Image.Height) {
		return variantSpec{}, false, nil
	}

	return spec, true, nil
}

func (s *Server) parseSize(raw, name string) (int, error) {
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	if value > s.cfg.MaxVariantDimension {
		return 0, fmt.Errorf("%s must be at most %d", name, s.cfg.MaxVariantDimension)
	}
	if !s.cfg.SizeAllowed(value) {
		if len(s.cfg.AllowedSizes) == 0 {
			return 0, fmt.Errorf("%s is not valid", name)
		}
		allowed := make([]string, len(s.cfg.AllowedSizes))
		for i, size := range s.cfg.AllowedSizes {
			allowed[i] = strconv.Itoa(size)
		}
		return 0, fmt.Errorf("%s must be one of: %s", name, strings.Join(allowed, ", "))
	}
	return value, nil
}

func (s *Server) writeImageHeaders(w http.ResponseWriter, mime, entityTag string) {
	h := w.Header()
	h.Set("Content-Type", mime)
	h.Set("Cache-Control", "public, max-age=31536000, immutable")
	h.Set("ETag", entityTag)
	h.Set("X-Robots-Tag", "noindex, nofollow")
	h.Set("Cross-Origin-Resource-Policy", "cross-origin")
	h.Set("Content-Disposition", "inline")
}

func formatFromName(name string) string {
	switch strings.TrimPrefix(strings.ToLower(filepath.Ext(name)), ".") {
	case "jpg", "jpeg":
		return "jpeg"
	case "png":
		return "png"
	case "webp":
		return "webp"
	case "avif":
		return "avif"
	case "gif":
		return "gif"
	default:
		return ""
	}
}
