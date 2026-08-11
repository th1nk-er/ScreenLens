package screenshot

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"strconv"
	"strings"

	kscreenshot "github.com/kbinani/screenshot"
	"github.com/th1nk-er/ScreenLens/internal/config"
	xdraw "golang.org/x/image/draw"
)

type Capture interface {
	Screenshot() ([]byte, error)
}

type Capturer struct {
	displayIndex int
	format       string
	quality      int
	maxWidth     int
	maxHeight    int
	maxBytes     int
}

func New(cfg config.CaptureConfig) (*Capturer, error) {
	index := 0
	monitor := strings.ToLower(strings.TrimSpace(cfg.Monitor))
	if monitor != "" && monitor != "primary" {
		parsed, err := strconv.Atoi(monitor)
		if err != nil || parsed < 0 {
			return nil, fmt.Errorf("invalid display index %q", cfg.Monitor)
		}
		index = parsed
	}
	format := strings.ToLower(strings.TrimSpace(cfg.Format))
	if format == "jpg" {
		format = "jpeg"
	}
	if format != "jpeg" && format != "png" {
		return nil, fmt.Errorf("unsupported screenshot format %q", cfg.Format)
	}
	quality := cfg.Quality
	if quality == 0 {
		quality = 85
	}
	maxWidth := cfg.MaxWidth
	if maxWidth == 0 {
		maxWidth = 2560
	}
	maxHeight := cfg.MaxHeight
	if maxHeight == 0 {
		maxHeight = 1440
	}
	maxBytes := cfg.MaxBytes
	if maxBytes == 0 {
		maxBytes = 7 * 1024 * 1024
	}
	if maxWidth < 1 || maxHeight < 1 || maxBytes < 1 {
		return nil, fmt.Errorf("screenshot size limits must be positive")
	}
	return &Capturer{
		displayIndex: index,
		format:       format,
		quality:      quality,
		maxWidth:     maxWidth,
		maxHeight:    maxHeight,
		maxBytes:     maxBytes,
	}, nil
}

func (c *Capturer) Screenshot() ([]byte, error) {
	imageData, err := kscreenshot.CaptureDisplay(c.displayIndex)
	if err != nil {
		return nil, fmt.Errorf("capture display %d: %w", c.displayIndex, err)
	}
	var source image.Image = resizeToFit(imageData, c.maxWidth, c.maxHeight)
	quality := c.quality
	for {
		encoded, encodeErr := c.encode(source, quality)
		if encodeErr != nil {
			return nil, encodeErr
		}
		if c.maxBytes <= 0 || len(encoded) <= c.maxBytes {
			return encoded, nil
		}

		if c.format == "jpeg" && quality > 25 {
			quality -= 10
			continue
		}
		bounds := source.Bounds()
		width, height := bounds.Dx(), bounds.Dy()
		nextWidth, nextHeight := max(1, width*4/5), max(1, height*4/5)
		if nextWidth == width && nextHeight == height {
			return nil, fmt.Errorf("encoded screenshot exceeds max_bytes (%d bytes)", c.maxBytes)
		}
		source = resizeToFit(source, nextWidth, nextHeight)
		quality = c.quality
	}
}

func (c *Capturer) encode(source image.Image, quality int) ([]byte, error) {
	var encoded bytes.Buffer
	var err error
	switch c.format {
	case "png":
		err = png.Encode(&encoded, source)
	default:
		err = jpeg.Encode(&encoded, source, &jpeg.Options{Quality: quality})
	}
	if err != nil {
		return nil, fmt.Errorf("encode screenshot as %s: %w", c.format, err)
	}
	return encoded.Bytes(), nil
}

func resizeToFit(source image.Image, maxWidth, maxHeight int) image.Image {
	if source == nil || maxWidth <= 0 || maxHeight <= 0 {
		return source
	}
	bounds := source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width <= maxWidth && height <= maxHeight {
		return source
	}
	scale := float64(maxWidth) / float64(width)
	if heightScale := float64(maxHeight) / float64(height); heightScale < scale {
		scale = heightScale
	}
	newWidth := max(1, int(float64(width)*scale))
	newHeight := max(1, int(float64(height)*scale))
	destination := image.NewRGBA(image.Rect(0, 0, newWidth, newHeight))
	xdraw.CatmullRom.Scale(destination, destination.Bounds(), source, bounds, xdraw.Over, nil)
	return destination
}

func (c *Capturer) MIMEType() string {
	if c.format == "png" {
		return "image/png"
	}
	return "image/jpeg"
}
