package screenshot

import (
	"image"
	"testing"
)

func TestResizeToFitPreservesAspectRatio(t *testing.T) {
	source := image.NewRGBA(image.Rect(0, 0, 4000, 2000))
	resized := resizeToFit(source, 1000, 1000)
	if got := resized.Bounds().Size(); got.X != 1000 || got.Y != 500 {
		t.Fatalf("resized dimensions = %v, want (1000, 500)", got)
	}
}

func TestResizeToFitDoesNotUpscale(t *testing.T) {
	source := image.NewRGBA(image.Rect(0, 0, 640, 480))
	resized := resizeToFit(source, 1920, 1080)
	if got := resized.Bounds().Size(); got != source.Bounds().Size() {
		t.Fatalf("resized dimensions = %v, want %v", got, source.Bounds().Size())
	}
}
