//go:build darwin || linux

package instance

import (
	"errors"
	"testing"
)

func TestAcquireRejectsSecondInstance(t *testing.T) {
	first, err := Acquire("ScreenLens test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Close() }()

	second, err := Acquire("ScreenLens test")
	if !errors.Is(err, ErrAlreadyRunning) {
		if second != nil {
			_ = second.Close()
		}
		t.Fatalf("second Acquire error = %v, want %v", err, ErrAlreadyRunning)
	}
}
