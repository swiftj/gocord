package media

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/gif"
	"math"
)

// MaxFitPixels bounds the images Fit will decode (about 120 megapixels, or
// roughly 480 MB of RGBA), so one huge file cannot exhaust memory.
const MaxFitPixels = 120_000_000

// fitMinDimension is the smallest longest edge Fit will shrink to.
const fitMinDimension = 640

// ErrCannotFit means an image could not be made small enough.
var ErrCannotFit = errors.New("media: image cannot be shrunk to fit")

// Fit returns b re-encoded to at most budget bytes. It keeps the original
// bytes when they already fit and opts asks for no change. Otherwise it
// applies opts (max dimension, format), then, while the result is too large,
// re-encodes as JPEG (or PNG when the image has transparency, so transparent
// areas are not flattened) and scales the longest edge down step by step, no
// lower than 640 pixels. budget <= 0 means no size limit. Animated GIFs are
// never re-encoded: they are returned as given or fail with ErrCannotFit.
func Fit(b []byte, filename string, budget int64, opts FetchOptions) (*Fetched, error) {
	ct, err := Sniff(b)
	if err != nil {
		return nil, err
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("media: decode %s header: %w", ct, err)
	}
	fits := func(n int) bool { return budget <= 0 || int64(n) <= budget }
	explicit := opts.MaxDimension > 0 || (opts.Format != "" && opts.Format != "original")
	if !explicit && fits(len(b)) {
		return &Fetched{Bytes: b, ContentType: ct, Filename: filename, Width: cfg.Width, Height: cfg.Height}, nil
	}
	if ct == TypeGIF {
		if g, err := gif.DecodeAll(bytes.NewReader(b)); err == nil && len(g.Image) > 1 {
			if !explicit && fits(len(b)) {
				return &Fetched{Bytes: b, ContentType: ct, Filename: filename, Width: cfg.Width, Height: cfg.Height}, nil
			}
			return nil, fmt.Errorf("%w: %s is an animated GIF (%d bytes); gocord does not re-encode animations", ErrCannotFit, filename, len(b))
		}
	}
	if int64(cfg.Width)*int64(cfg.Height) > MaxFitPixels {
		return nil, fmt.Errorf("%w: %s is %d×%d, over the %d-megapixel limit for resizing", ErrCannotFit, filename, cfg.Width, cfg.Height, MaxFitPixels/1_000_000)
	}

	// First apply what the caller asked for.
	f, err := Transform(b, filename, opts)
	if err != nil {
		return nil, err
	}
	if fits(len(f.Bytes)) {
		return f, nil
	}

	// Still too large: pick a lossy-friendly format and shrink.
	src, _, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("media: decode %s: %w", ct, err)
	}
	format := "jpeg"
	if !opaque(src) {
		format = "png"
	}
	if opts.Format != "" && opts.Format != "original" {
		format = opts.Format
	}
	dim := max(f.Width, f.Height)
	for range 8 {
		f, err = Transform(b, filename, FetchOptions{MaxDimension: dim, Format: format, Quality: opts.Quality})
		if err != nil {
			return nil, err
		}
		if fits(len(f.Bytes)) {
			return f, nil
		}
		if dim <= fitMinDimension {
			break
		}
		// Encoded size grows roughly with pixel count, so scale the edge by
		// the square root of the overshoot, with a margin.
		scale := math.Sqrt(float64(budget)/float64(len(f.Bytes))) * 0.92
		dim = max(fitMinDimension, min(dim-1, int(float64(dim)*scale)))
	}
	return nil, fmt.Errorf("%w: %s is still %d bytes at %d×%d, over the %d-byte limit", ErrCannotFit, filename, len(f.Bytes), f.Width, f.Height, budget)
}

// opaque reports whether img has no transparent pixels.
func opaque(img image.Image) bool {
	if o, ok := img.(interface{ Opaque() bool }); ok {
		return o.Opaque()
	}
	return false
}
