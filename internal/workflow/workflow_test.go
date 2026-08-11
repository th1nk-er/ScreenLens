package workflow

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type recordingSender struct {
	mu       sync.Mutex
	events   []string
	replyTo  int
	done     chan struct{}
	photoErr error
}

func (s *recordingSender) SendText(context.Context, string, string) error {
	s.mu.Lock()
	s.events = append(s.events, "text")
	s.mu.Unlock()
	select {
	case s.done <- struct{}{}:
	default:
	}
	return nil
}

func (s *recordingSender) SendPhoto(context.Context, string, []byte, string) (int, error) {
	s.mu.Lock()
	s.events = append(s.events, "photo")
	s.mu.Unlock()
	if s.photoErr != nil {
		return 0, s.photoErr
	}
	return 42, nil
}

func (s *recordingSender) SendReply(context.Context, string, string, int) error {
	s.mu.Lock()
	s.events = append(s.events, "reply")
	s.replyTo = 42
	s.mu.Unlock()
	select {
	case s.done <- struct{}{}:
	default:
	}
	return nil
}

func (s *recordingSender) snapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.events...)
}

type recordingCapture struct{}

func (recordingCapture) Screenshot() ([]byte, error) { return []byte("image"), nil }

type orderedVision struct {
	sender *recordingSender
	seen   chan []string
}

func (v orderedVision) Analyze(context.Context, []byte, string) (string, error) {
	v.seen <- v.sender.snapshot()
	return "analysis", nil
}

func TestScreenshotIsDeliveredBeforeAnalysis(t *testing.T) {
	sender := &recordingSender{done: make(chan struct{}, 1)}
	vision := orderedVision{sender: sender, seen: make(chan []string, 1)}
	engine := New(recordingCapture{}, vision, sender, "prompt", true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = engine.Run(ctx) }()

	if err := engine.CaptureFrom(ctx, "123", "hotkey"); err != nil {
		t.Fatal(err)
	}
	select {
	case seen := <-vision.seen:
		if len(seen) != 1 || seen[0] != "photo" {
			t.Fatalf("events seen by vision = %v, want [photo]", seen)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for vision request")
	}
	select {
	case <-sender.done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for result delivery")
	}
	if events := sender.snapshot(); len(events) != 2 || events[1] != "reply" {
		t.Fatalf("sender events = %v, want [photo reply]", events)
	}
	if sender.replyTo != 42 {
		t.Fatalf("replyTo = %d, want 42", sender.replyTo)
	}
}

func TestCaptureRequestsAreCoalesced(t *testing.T) {
	engine := New(recordingCapture{}, orderedVision{}, &recordingSender{done: make(chan struct{}, 1)}, "prompt", false)
	ctx := context.Background()
	if err := engine.CaptureFrom(ctx, "123", "first"); err != nil {
		t.Fatal(err)
	}
	if err := engine.CaptureFrom(ctx, "123", "second"); !errors.Is(err, ErrCaptureInProgress) {
		t.Fatalf("second capture error = %v, want ErrCaptureInProgress", err)
	}
}

func TestScreenshotDeliveryWarningSurvivesSuccessfulAnalysis(t *testing.T) {
	sender := &recordingSender{done: make(chan struct{}, 1), photoErr: errors.New("upload failed")}
	engine := New(recordingCapture{}, orderedVision{sender: sender, seen: make(chan []string, 1)}, sender, "prompt", true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = engine.Run(ctx) }()

	if err := engine.CaptureFrom(ctx, "123", "test"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-sender.done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for analysis delivery")
	}
	status := engine.Status()
	if status.LastError != "" {
		t.Fatalf("LastError = %q, want empty after successful analysis delivery", status.LastError)
	}
	if status.LastWarning == "" {
		t.Fatal("LastWarning is empty, want screenshot delivery warning")
	}
}
