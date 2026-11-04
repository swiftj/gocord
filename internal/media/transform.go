package media

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"net/http"
	"path"
	"strings"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // registers the WebP decoder with image.Decode
)

// Image MIME types accepted for every operation.
const (
	TypePNG  = "image/png"
	TypeJPEG = "image/jpeg"
	TypeGIF  = "image/gif"
	TypeWebP = "image/webp"
)

// AcceptedTypes lists the image types media accepts.
var AcceptedTypes = []string{TypePNG, TypeJPEG, TypeGIF, TypeWebP}

// FetchOptions controls resizing and re-encoding.
type FetchOptions struct {
	// MaxDimension scales the longest edge down to this many pixels (never
	// up). 0 keeps the original size.
	MaxDimension int
	// Format is "", "jpeg", "png", or "webp". "" keeps the original encoding
	// (and the original bytes when no resize is needed). Go has no pure WebP
	// encoder, so "webp" produces PNG; Fetched.ContentType reports the real type.
	Format string
	// Quality is the JPEG quality (1-100); 0 means DefaultQuality.
	Quality int
	// StripGIFToFirstFrame allows re-encoding an animated GIF as its first
	// frame. Without it, re-encoding an animated GIF is an error.
	StripGIFToFirstFrame bool
}

// Fetched is a downloaded (and possibly transformed) image.
type Fetched struct {
	Bytes         []byte
	ContentType   string
	Filename      string
	Width, Height int
	// SourceURL is the URL the bytes came from. It may be a signed URL:
	// return it to the caller, never log it.
	SourceURL string
}

// Sniff detects the image type from bytes, ignoring any declared header.
func Sniff(b []byte) (string, error) {
	ct := http.DetectContentType(b)
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	for _, t := range AcceptedTypes {
		if ct == t {
			return ct, nil
		}
	}
	return ct, fmt.Errorf("%w: %s (accepted: %s)", ErrUnsupportedType, ct, strings.Join(AcceptedTypes, ", "))
}

// ScaledSize fits w×h inside limit on the longest edge, preserving aspect ratio
// and never scaling up. limit <= 0 returns the input.
func ScaledSize(w, h, limit int) (int, int) {
	if limit <= 0 || w <= 0 || h <= 0 || (w <= limit && h <= limit) {
		return w, h
	}
	if w >= h {
		return limit, max(1, (h*limit+w/2)/w)
	}
	return max(1, (w*limit+h/2)/h), limit
}

// Transform sniffs b and applies opts. filename is the source name; its
// extension is updated when the encoding changes.
func Transform(b []byte, filename string, opts FetchOptions) (*Fetched, error) {
	ct, err := Sniff(b)
	if err != nil {
		return nil, err
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("media: decode %s header: %w", ct, err)
	}
	w, h := ScaledSize(cfg.Width, cfg.Height, opts.MaxDimension)
	resize := w != cfg.Width || h != cfg.Height

	target, err := targetType(ct, opts.Format)
	if err != nil {
		return nil, err
	}
	if !resize && target == ct {
		return &Fetched{Bytes: b, ContentType: ct, Filename: filename, Width: cfg.Width, Height: cfg.Height}, nil
	}
	if target == TypeWebP { // a WebP that must be re-encoded becomes PNG
		target = TypePNG
	}

	if ct == TypeGIF {
		if g, err := gif.DecodeAll(bytes.NewReader(b)); err == nil && len(g.Image) > 1 && !opts.StripGIFToFirstFrame {
			return nil, fmt.Errorf("media: re-encoding an animated GIF keeps only its first frame; use format \"original\" with no resize, or allow the first frame")
		}
	}
	src, _, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("media: decode %s: %w", ct, err)
	}
	img := src
	if resize {
		dst := image.NewRGBA(image.Rect(0, 0, w, h))
		draw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Over, nil)
		img = dst
	}
	out, err := encode(img, target, opts.Quality)
	if err != nil {
		return nil, err
	}
	bounds := img.Bounds()
	return &Fetched{Bytes: out, ContentType: target, Filename: withExt(filename, target), Width: bounds.Dx(), Height: bounds.Dy()}, nil
}

// targetType maps a Format to the output MIME type.
func targetType(src, format string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", "original":
		return src, nil
	case "jpeg", "jpg":
		return TypeJPEG, nil
	case "png", "webp":
		return TypePNG, nil
	}
	return "", fmt.Errorf("media: unknown format %q (want jpeg, png, webp, or original)", format)
}

func encode(img image.Image, ct string, quality int) ([]byte, error) {
	var buf bytes.Buffer
	switch ct {
	case TypeJPEG:
		if quality <= 0 {
			quality = DefaultQuality
		}
		quality = min(max(quality, 1), 100)
		if err := jpeg.Encode(&buf, flatten(img), &jpeg.Options{Quality: quality}); err != nil {
			return nil, fmt.Errorf("media: encode jpeg: %w", err)
		}
	case TypeGIF:
		if err := gif.Encode(&buf, img, nil); err != nil {
			return nil, fmt.Errorf("media: encode gif: %w", err)
		}
	default: // PNG, and WebP sources that had to be re-encoded
		if err := png.Encode(&buf, img); err != nil {
			return nil, fmt.Errorf("media: encode png: %w", err)
		}
	}
	return buf.Bytes(), nil
}

// flatten composites img over white so transparent areas don't turn black in JPEG.
func flatten(img image.Image) image.Image {
	b := img.Bounds()
	dst := image.NewRGBA(b)
	draw.Draw(dst, b, &image.Uniform{C: color.White}, image.Point{}, draw.Src)
	draw.Draw(dst, b, img, b.Min, draw.Over)
	return dst
}

func withExt(name, ct string) string {
	ext := map[string]string{TypeJPEG: ".jpg", TypePNG: ".png", TypeGIF: ".gif", TypeWebP: ".webp"}[ct]
	if name == "" {
		return "image" + ext
	}
	return strings.TrimSuffix(name, path.Ext(name)) + ext
}
