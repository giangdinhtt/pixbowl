package imageproc

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"math"
	"sync"

	"github.com/davidbyttow/govips/v2/vips"

	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	_ "golang.org/x/image/webp"
)

// Errors returned by Process. Callers map them to HTTP status codes.
var (
	ErrUnsupported    = errors.New("unsupported image format")
	ErrTooLarge       = errors.New("image is too large")
	ErrFormatDisabled = errors.New("output format not supported by this build")
)

type ImageError struct {
	Kind error
	Msg  string
}

func (e *ImageError) Error() string { return e.Msg }
func (e *ImageError) Unwrap() error { return e.Kind }

// RuntimeConfig is the subset of server configuration the pipeline needs.
type RuntimeConfig struct {
	Concurrency   int
	OutputFormat  string
	Quality       int
	MaxDimension  int
	MaxPixels     int
	StripMetadata bool
}

// Options describes a single transformation.
type Options struct {
	Format        string // target format, "preserve" or "" keeps the source
	Width         int    // variant box, 0 = unconstrained
	Height        int
	Fit           string // inside | cover | contain
	MaxDimension  int
	Quality       int
	StripMetadata bool
	MaxPixels     int
}

// Result is the encoded output plus the facts the metadata file needs.
type Result struct {
	Data         []byte
	Format       string
	Mime         string
	Width        int
	Height       int
	Animated     bool
	SourceFormat string
	SourceWidth  int
	SourceHeight int
}

var (
	startOnce     sync.Once
	startErr      error
	encodeSupport = map[string]bool{"jpeg": true, "png": true, "webp": true}
)

// Start boots libvips exactly once and discovers which encoders this build has.
// A distro libvips may be compiled without AVIF, so we cannot assume it exists.
func Start(cfg RuntimeConfig) error {
	startOnce.Do(func() {
		concurrency := cfg.Concurrency
		if concurrency < 1 {
			concurrency = 1
		}
		// Silence libvips' chatty startup before it runs.
		vips.LoggingSettings(nil, vips.LogLevelError)
		if err := vips.Startup(&vips.Config{ConcurrencyLevel: concurrency}); err != nil {
			startErr = fmt.Errorf("libvips: %w", err)
			return
		}
		vips.LoggingSettings(nil, vips.LogLevelError)

		for _, format := range []string{"jpeg", "png", "webp", "avif"} {
			encodeSupport[format] = canSave(format)
		}
		if cfg.OutputFormat == "avif" && !encodeSupport["avif"] {
			startErr = fmt.Errorf("OUTPUT_FORMAT=avif but this libvips build has no AVIF encoder (heifsave)")
		}
	})
	return startErr
}

func Stop() { vips.Shutdown() }

// SupportsFormat reports whether this build can encode the given format.
func SupportsFormat(format string) bool { return encodeSupport[format] }

// Formats returns the list of encodable formats.
func Formats() []string {
	out := make([]string, 0, len(encodeSupport))
	for _, format := range []string{"jpeg", "png", "webp", "avif"} {
		if encodeSupport[format] {
			out = append(out, format)
		}
	}
	return out
}

var mimeByFormat = map[string]string{
	"jpeg": "image/jpeg",
	"png":  "image/png",
	"webp": "image/webp",
	"avif": "image/avif",
	"gif":  "image/gif",
	"tiff": "image/tiff",
	"heif": "image/heif",
}

var extByFormat = map[string]string{
	"jpeg": "jpg",
	"png":  "png",
	"webp": "webp",
	"avif": "avif",
	"gif":  "gif",
	"tiff": "tiff",
	"heif": "heif",
}

func Ext(format string) string { return extByFormat[format] }

// Mime returns the content type for an encoded format.
func Mime(format string) string {
	if mime, ok := mimeByFormat[format]; ok {
		return mime
	}
	return "application/octet-stream"
}

// Version exposes the libvips version for /healthz.
func Version() string { return vips.Version }

var inputAllowed = map[vips.ImageType]bool{
	vips.ImageTypeJPEG: true,
	vips.ImageTypePNG:  true,
	vips.ImageTypeWEBP: true,
	vips.ImageTypeGIF:  true,
	vips.ImageTypeTIFF: true,
	vips.ImageTypeHEIF: true,
	vips.ImageTypeAVIF: true,
}

func formatName(t vips.ImageType) string {
	switch t {
	case vips.ImageTypeJPEG:
		return "jpeg"
	case vips.ImageTypePNG:
		return "png"
	case vips.ImageTypeWEBP:
		return "webp"
	case vips.ImageTypeGIF:
		return "gif"
	case vips.ImageTypeTIFF:
		return "tiff"
	case vips.ImageTypeHEIF:
		return "heif"
	case vips.ImageTypeAVIF:
		return "avif"
	default:
		return "unknown"
	}
}

// Process decodes src, applies resize/format changes and returns encoded bytes.
// Animated images are passed through untouched: converting them would drop
// frames, and resizing every frame is not worth the CPU on a small VPS.
func Process(src []byte, opt Options) (*Result, error) {
	if opt.Quality <= 0 {
		opt.Quality = 82
	}
	if opt.MaxPixels <= 0 {
		opt.MaxPixels = 40_000_000
	}
	if opt.Fit == "" {
		opt.Fit = "inside"
	}

	kind := vips.DetermineImageType(src)
	sourceFormat := formatName(kind)
	if !inputAllowed[kind] {
		return nil, &ImageError{Kind: ErrUnsupported, Msg: "Unsupported image format (accepted: JPEG, PNG, WebP, GIF, TIFF, HEIC, AVIF)."}
	}

	// Read dimensions from the header before libvips allocates anything, to
	// defuse decompression bombs (a 5MB PNG can decode to gigabytes).
	if width, height, ok := headerDimensions(src); ok {
		if int64(width)*int64(height) > int64(opt.MaxPixels) {
			return nil, &ImageError{Kind: ErrTooLarge, Msg: "Image exceeds the pixel limit."}
		}
	}

	ref, err := vips.NewImageFromBuffer(src)
	if err != nil {
		return nil, &ImageError{Kind: ErrUnsupported, Msg: "Could not read the image (corrupt file or unknown format)."}
	}
	defer ref.Close()

	meta := ref.Metadata()
	if meta.Width <= 0 || meta.Height <= 0 {
		return nil, &ImageError{Kind: ErrUnsupported, Msg: "Could not read the image dimensions."}
	}
	if int64(meta.Width)*int64(meta.Height) > int64(opt.MaxPixels) {
		return nil, &ImageError{Kind: ErrTooLarge, Msg: "Image exceeds the pixel limit."}
	}
	if meta.Format != vips.ImageTypeUnknown {
		sourceFormat = formatName(meta.Format)
	}

	result := &Result{
		SourceFormat: sourceFormat,
		SourceWidth:  meta.Width,
		SourceHeight: meta.Height,
		Animated:     meta.Pages > 1,
	}
	if result.Animated {
		result.Data = src
		result.Format = sourceFormat
		result.Mime = mimeByFormat[sourceFormat]
		result.Width = meta.Width
		result.Height = meta.Height
		return result, nil
	}

	// Apply EXIF orientation. Ignore failures: orientation is cosmetic.
	_ = ref.AutoRotate()
	meta = ref.Metadata()
	srcW, srcH := meta.Width, meta.Height
	result.SourceWidth, result.SourceHeight = srcW, srcH

	if opt.StripMetadata {
		// Convert to sRGB before dropping the ICC profile, otherwise wide-gamut
		// photos would render dull in browsers.
		_ = ref.ToColorSpace(vips.InterpretationSRGB)
	}

	target := opt.Format
	if target == "" || target == "preserve" {
		target = sourceFormat
	}
	target = normalizeTarget(target)
	if !SupportsFormat(target) {
		return nil, &ImageError{Kind: ErrFormatDisabled, Msg: fmt.Sprintf("This server cannot encode %s.", target)}
	}

	tw, th, padW, padH, crop := geometry(srcW, srcH, opt)
	if tw != srcW || th != srcH {
		interesting := vips.InterestingNone
		if crop {
			interesting = vips.InterestingCentre
		}
		if err := ref.Thumbnail(tw, th, interesting); err != nil {
			return nil, &ImageError{Kind: ErrUnsupported, Msg: "Could not resize the image."}
		}
	}
	if padW > tw || padH > th {
		left := (padW - tw) / 2
		top := (padH - th) / 2
		if err := ref.EmbedBackground(left, top, padW, padH, &vips.Color{R: 255, G: 255, B: 255}); err != nil {
			return nil, &ImageError{Kind: ErrUnsupported, Msg: "Could not pad the image."}
		}
	}

	data, err := encode(ref, target, opt.Quality, opt.StripMetadata)
	if err != nil {
		return nil, &ImageError{Kind: ErrUnsupported, Msg: "Could not encode the image to the target format."}
	}

	final := ref.Metadata()
	result.Data = data
	result.Format = target
	result.Mime = mimeByFormat[target]
	result.Width = final.Width
	result.Height = final.Height
	return result, nil
}

func normalizeTarget(format string) string {
	switch format {
	case "jpeg", "png", "webp", "avif":
		return format
	default:
		// GIF/TIFF/HEIF are not sensible public web formats; re-encode to PNG.
		return "png"
	}
}

// geometry returns the thumbnail box, an optional padding canvas and whether
// libvips should centre-crop. Sizes are clamped to the source so we never
// upscale (vips Thumbnail happily enlarges when asked).
func geometry(srcW, srcH int, opt Options) (tw, th, padW, padH int, crop bool) {
	if opt.Width <= 0 && opt.Height <= 0 {
		if opt.MaxDimension > 0 && max(srcW, srcH) > opt.MaxDimension {
			scale := float64(opt.MaxDimension) / float64(max(srcW, srcH))
			return scaled(srcW, srcW, scale), scaled(srcH, srcH, scale), 0, 0, false
		}
		return srcW, srcH, 0, 0, false
	}

	w, h := opt.Width, opt.Height

	if opt.Fit == "cover" && w > 0 && h > 0 {
		// "cover" promises an exact box, so allow modest upscaling (max 2x)
		// instead of silently returning a different shape.
		if limit := srcW * 2; w > limit {
			w = limit
		}
		if limit := srcH * 2; h > limit {
			h = limit
		}
		return w, h, 0, 0, true
	}

	// Everything else never enlarges.
	if w > 0 && w > srcW {
		w = srcW
	}
	if h > 0 && h > srcH {
		h = srcH
	}

	if opt.Fit == "contain" {
		boxW, boxH := w, h
		if boxW <= 0 {
			boxW = srcW
		}
		if boxH <= 0 {
			boxH = srcH
		}
		iw, ih := fitInside(srcW, srcH, boxW, boxH)
		return iw, ih, boxW, boxH, false
	}

	iw, ih := fitInside(srcW, srcH, w, h)
	return iw, ih, 0, 0, false
}

func fitInside(srcW, srcH, w, h int) (int, int) {
	scale := 1.0
	if w > 0 {
		scale = math.Min(scale, float64(w)/float64(srcW))
	}
	if h > 0 {
		scale = math.Min(scale, float64(h)/float64(srcH))
	}
	if scale >= 1 {
		return srcW, srcH
	}
	return scaled(srcW, srcW, scale), scaled(srcH, srcH, scale)
}

func scaled(size, total int, scale float64) int {
	value := int(math.Round(float64(size) * scale))
	if value < 1 {
		return 1
	}
	if value > total {
		return total
	}
	return value
}

func encode(ref *vips.ImageRef, format string, quality int, strip bool) ([]byte, error) {
	switch format {
	case "jpeg":
		if ref.HasAlpha() {
			if err := ref.Flatten(&vips.Color{R: 255, G: 255, B: 255}); err != nil {
				return nil, err
			}
		}
		params := vips.NewJpegExportParams()
		params.Quality = quality
		params.StripMetadata = strip
		params.Interlace = true
		params.OptimizeCoding = true
		data, _, err := ref.ExportJpeg(params)
		return data, err
	case "webp":
		params := vips.NewWebpExportParams()
		params.Quality = quality
		params.StripMetadata = strip
		params.ReductionEffort = 4
		data, _, err := ref.ExportWebp(params)
		return data, err
	case "png":
		params := vips.NewPngExportParams()
		params.Compression = 9
		params.StripMetadata = strip
		data, _, err := ref.ExportPng(params)
		return data, err
	case "avif":
		params := vips.NewAvifExportParams()
		params.Quality = quality
		params.StripMetadata = strip
		params.Effort = 4
		data, _, err := ref.ExportAvif(params)
		return data, err
	default:
		return nil, ErrFormatDisabled
	}
}

// headerDimensions decodes only the image header (cheap, no pixel data).
func headerDimensions(src []byte) (int, int, bool) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(src))
	if err != nil {
		return 0, 0, false
	}
	return cfg.Width, cfg.Height, true
}
