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

const (
	defaultDisplayIndex    = 0
	minimumImageSize       = 1
	minimumJPEGQuality     = 25
	qualityReduction       = 10
	resizeScaleNumerator   = 4
	resizeScaleDenominator = 5
)

type Capture interface {
	Screenshot() ([]byte, error)
}

// RegionCapture is implemented by capturers that can capture an arbitrary
// desktop rectangle without showing a selection overlay.
type RegionCapture interface {
	ScreenshotRegion(start, end image.Point) ([]byte, error)
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
	index := defaultDisplayIndex
	monitor := strings.ToLower(strings.TrimSpace(cfg.Monitor))
	if monitor != "" && monitor != config.MonitorPrimary {
		parsed, err := strconv.Atoi(monitor)
		if err != nil || parsed < 0 {
			return nil, fmt.Errorf("invalid display index %q", cfg.Monitor)
		}
		index = parsed
	}
	format := strings.ToLower(strings.TrimSpace(cfg.Format))
	if format == config.FormatJPG {
		format = config.FormatJPEG
	}
	if format != config.FormatJPEG && format != config.FormatPNG {
		return nil, fmt.Errorf("unsupported screenshot format %q", cfg.Format)
	}
	quality := cfg.Quality
	if quality == 0 {
		quality = config.DefaultCaptureQuality
	}
	maxWidth := cfg.MaxWidth
	if maxWidth == 0 {
		maxWidth = config.DefaultCaptureMaxWidth
	}
	maxHeight := cfg.MaxHeight
	if maxHeight == 0 {
		maxHeight = config.DefaultCaptureMaxHeight
	}
	maxBytes := cfg.MaxBytes
	if maxBytes == 0 {
		maxBytes = config.DefaultCaptureMaxBytes
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
	return c.encodeWithLimits(imageData)
}

// ScreenshotRegion captures the rectangle formed by start and end. image.Rect
// normalizes either point order, so callers can use any two corners.
func (c *Capturer) ScreenshotRegion(start, end image.Point) ([]byte, error) {
	rect := regionRectangle(start, end)
	if rect.Dx() < minimumImageSize || rect.Dy() < minimumImageSize {
		return nil, fmt.Errorf("region screenshot requires two distinct points")
	}
	imageData, err := kscreenshot.CaptureRect(rect)
	if err != nil {
		return nil, fmt.Errorf("capture screenshot region %v: %w", rect, err)
	}
	return c.encodeWithLimits(imageData)
}

func regionRectangle(start, end image.Point) image.Rectangle {
	return image.Rectangle{
		Min: image.Point{X: min(start.X, end.X), Y: min(start.Y, end.Y)},
		Max: image.Point{X: max(start.X, end.X), Y: max(start.Y, end.Y)},
	}
}

func (c *Capturer) encodeWithLimits(imageData image.Image) ([]byte, error) {
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

		if c.format == config.FormatJPEG && quality > minimumJPEGQuality {
			quality -= qualityReduction
			continue
		}
		bounds := source.Bounds()
		width, height := bounds.Dx(), bounds.Dy()
		nextWidth := max(minimumImageSize, width*resizeScaleNumerator/resizeScaleDenominator)
		nextHeight := max(minimumImageSize, height*resizeScaleNumerator/resizeScaleDenominator)
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
	case config.FormatPNG:
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
	newWidth := max(minimumImageSize, int(float64(width)*scale))
	newHeight := max(minimumImageSize, int(float64(height)*scale))
	destination := image.NewRGBA(image.Rect(0, 0, newWidth, newHeight))
	xdraw.CatmullRom.Scale(destination, destination.Bounds(), source, bounds, xdraw.Over, nil)
	return destination
}

func (c *Capturer) MIMEType() string {
	if c.format == config.FormatPNG {
		return config.MIMETypePNG
	}
	return config.MIMETypeJPEG
}
