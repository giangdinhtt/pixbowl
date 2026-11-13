package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/giangdinhtt/pixbowl/internal/id"
	"github.com/giangdinhtt/pixbowl/internal/imageproc"
	"github.com/giangdinhtt/pixbowl/internal/store"
)

type uploadResult struct {
	ID           string `json:"id"`
	URL          string `json:"url"`
	OriginalURL  string `json:"original_url,omitempty"`
	DeleteURL    string `json:"delete_url"`
	DeleteToken  string `json:"delete_token"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	Size         int64  `json:"size"`
	Format       string `json:"format"`
	Animated     bool   `json:"animated,omitempty"`
	OriginalSize int64  `json:"original_size,omitempty"`
}

type uploadFailure struct {
	Filename string `json:"filename,omitempty"`
	Error    string `json:"error"`
	Message  string `json:"message"`
}

type uploadResponse struct {
	Files   []uploadResult  `json:"files"`
	Errors  []uploadFailure `json:"errors,omitempty"`
	Error   string          `json:"error,omitempty"`
	Message string          `json:"message,omitempty"`
}

var errFileTooLarge = errors.New("file too large")

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if allowed, retry := s.uploadLimiter.Allow(s.clientIP(r)); !allowed {
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(retry.Seconds()))))
		s.writeError(w, http.StatusTooManyRequests, "rate_limited", "You are uploading too often, try again later.")
		return
	}

	if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "multipart/form-data") {
		s.writeError(w, http.StatusUnsupportedMediaType, "multipart_required", `Send images as multipart/form-data with a field named "file".`)
		return
	}

	if !s.acquireUpload(r.Context()) {
		w.Header().Set("Retry-After", "5")
		s.writeError(w, http.StatusServiceUnavailable, "busy", "The server is busy, try again in a few seconds.")
		return
	}
	defer s.releaseUpload()

	maxBody := s.cfg.MaxFileBytes*int64(s.cfg.MaxFiles) + (1 << 20)
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)

	reader, err := r.MultipartReader()
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid_multipart", "Could not read the multipart body.")
		return
	}

	var (
		files    []uploadResult
		failures []uploadFailure
	)

	for len(files)+len(failures) < s.cfg.MaxFiles {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			failures = append(failures, uploadFailure{Error: "invalid_multipart", Message: "The body was truncated or exceeded the limit."})
			break
		}
		if part.FormName() != "file" || part.FileName() == "" {
			part.Close()
			continue
		}

		filename := sanitizeFilename(part.FileName())
		data, readErr := readLimited(part, s.cfg.MaxFileBytes)
		part.Close()
		if readErr != nil {
			failures = append(failures, uploadFailure{
				Filename: filename,
				Error:    "file_too_large",
				Message:  fmt.Sprintf("Image exceeds %dMB.", s.cfg.MaxFileBytes>>20),
			})
			continue
		}

		result, saveErr := s.saveImage(r, data, filename)
		if saveErr != nil {
			failures = append(failures, failureFromError(filename, saveErr))
			s.log.Warn("upload.rejected", "filename", filename, "err", saveErr)
			continue
		}
		files = append(files, result)
	}

	switch {
	case len(files) == 1 && len(failures) == 0:
		s.writeJSON(w, http.StatusOK, files[0])
	case len(files) > 0:
		s.writeJSON(w, http.StatusOK, uploadResponse{Files: files, Errors: failures})
	case len(failures) > 0:
		status := http.StatusUnprocessableEntity
		if failures[0].Error == "file_too_large" || failures[0].Error == "invalid_multipart" {
			status = http.StatusRequestEntityTooLarge
		}
		s.writeJSON(w, status, uploadResponse{Files: []uploadResult{}, Errors: failures, Error: "no_file_uploaded", Message: "No image could be uploaded."})
	default:
		s.writeError(w, http.StatusBadRequest, "no_file", `No "file" field found in the request.`)
	}
}

func (s *Server) saveImage(r *http.Request, data []byte, filename string) (uploadResult, error) {
	ctx := r.Context()

	out, err := imageproc.Process(data, imageproc.Options{
		Format:        s.cfg.OutputFormat,
		MaxDimension:  s.cfg.MaxDimension,
		Quality:       s.cfg.Quality,
		StripMetadata: s.cfg.StripMetadata,
		MaxPixels:     s.cfg.MaxPixels,
	})
	if err != nil {
		return uploadResult{}, err
	}

	imageID, err := id.New(s.cfg.IDLength)
	if err != nil {
		return uploadResult{}, err
	}
	token, err := id.Token()
	if err != nil {
		return uploadResult{}, err
	}

	imageName := "image." + imageproc.Ext(out.Format)
	imageSHA := sha256Hex(out.Data)

	meta := &store.Meta{
		ID:              imageID,
		CreatedAt:       time.Now().UTC(),
		DeleteTokenHash: sha256Hex([]byte(token)),
		Filename:        filename,
		Animated:        out.Animated,
		SourceFormat:    out.SourceFormat,
		Image: store.Rendition{
			Name:   imageName,
			Mime:   out.Mime,
			Size:   int64(len(out.Data)),
			Width:  out.Width,
			Height: out.Height,
			SHA256: imageSHA,
		},
	}

	var originalSize int64
	if s.cfg.KeepOriginal {
		originalSHA := sha256Hex(data)
		if originalSHA != imageSHA {
			originalName := "original." + imageproc.Ext(out.SourceFormat)
			meta.Original = &store.Rendition{
				Name:   originalName,
				Mime:   imageproc.Mime(out.SourceFormat),
				Size:   int64(len(data)),
				Width:  out.SourceWidth,
				Height: out.SourceHeight,
				SHA256: originalSHA,
			}
			if err := s.store.Put(ctx, imageID, originalName, data); err != nil {
				return uploadResult{}, err
			}
			originalSize = int64(len(data))
		}
	}

	if err := s.store.Put(ctx, imageID, imageName, out.Data); err != nil {
		return uploadResult{}, err
	}
	// meta.json goes last: an interrupted upload leaves no visible image.
	if err := s.store.PutMeta(ctx, meta); err != nil {
		return uploadResult{}, err
	}

	base := s.baseURL(r)
	result := uploadResult{
		ID:          imageID,
		URL:         fmt.Sprintf("%s/i/%s.%s", base, imageID, imageproc.Ext(out.Format)),
		DeleteURL:   fmt.Sprintf("%s/d/%s#%s", base, imageID, token),
		DeleteToken: token,
		Width:       out.Width,
		Height:      out.Height,
		Size:        int64(len(out.Data)),
		Format:      out.Format,
		Animated:    out.Animated,
	}
	if meta.Original != nil {
		result.OriginalURL = fmt.Sprintf("%s/o/%s.%s", base, imageID, imageproc.Ext(out.SourceFormat))
		result.OriginalSize = originalSize
	}

	s.log.Info("image.stored",
		"id", imageID,
		"format", out.Format,
		"width", out.Width,
		"height", out.Height,
		"bytes", len(out.Data),
		"original_bytes", originalSize,
	)
	return result, nil
}

func (s *Server) acquireUpload(ctx context.Context) bool {
	select {
	case s.uploadSem <- struct{}{}:
		return true
	default:
	}

	if s.waiters.Load() >= int64(s.cfg.UploadQueueLimit) {
		return false
	}
	s.waiters.Add(1)
	defer s.waiters.Add(-1)

	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()

	select {
	case s.uploadSem <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	case <-timer.C:
		return false
	}
}

func (s *Server) releaseUpload() { <-s.uploadSem }

func readLimited(reader io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errFileTooLarge
	}
	return data, nil
}

func failureFromError(filename string, err error) uploadFailure {
	var imageErr *imageproc.ImageError
	if errors.As(err, &imageErr) {
		switch {
		case errors.Is(imageErr.Kind, imageproc.ErrTooLarge):
			return uploadFailure{Filename: filename, Error: "image_too_large", Message: imageErr.Msg}
		case errors.Is(imageErr.Kind, imageproc.ErrFormatDisabled):
			return uploadFailure{Filename: filename, Error: "format_unsupported", Message: imageErr.Msg}
		default:
			return uploadFailure{Filename: filename, Error: "unsupported_image", Message: imageErr.Msg}
		}
	}
	return uploadFailure{Filename: filename, Error: "upload_failed", Message: "Could not process the image."}
}
