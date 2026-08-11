package workflow

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/th1nk-er/ScreenLens/internal/analyzer"
)

type recordingSender struct {
	mu       sync.Mutex
	events   []string
	replyTo  int
	done     chan struct{}
	photoErr error
}

func (s *recordingSender) SendText(ctx context.Context, _, _ string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	s.events = append(s.events, "text")
	s.mu.Unlock()
	select {
	case s.done <- struct{}{}:
	default:
	}
	return nil
}

func (s *recordingSender) SendPhoto(ctx context.Context, _ string, _ []byte, _ string) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	s.mu.Lock()
	s.events = append(s.events, "photo")
	s.mu.Unlock()
	if s.photoErr != nil {
		return 0, s.photoErr
	}
	return 42, nil
}

func (s *recordingSender) SendReply(ctx context.Context, _, _ string, _ int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
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

func TestRunDiscardsQueuedCaptureWhenContextIsCanceled(t *testing.T) {
	engine := New(recordingCapture{}, orderedVision{}, &recordingSender{done: make(chan struct{}, 1)}, "prompt", false)
	ctx, cancel := context.WithCancel(context.Background())
	if err := engine.CaptureFrom(ctx, "123", "test"); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := engine.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if err := engine.CaptureFrom(context.Background(), "123", "test"); err != nil {
		t.Fatalf("capture after canceled run = %v", err)
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

type blockingAnalyzer struct {
	started chan struct{}
	once    sync.Once
}

func (a *blockingAnalyzer) Analyze(ctx context.Context, _ analyzer.Request) (analyzer.Result, error) {
	a.once.Do(func() { close(a.started) })
	<-ctx.Done()
	return analyzer.Result{}, ctx.Err()
}

func TestCancelStopsActiveAnalysis(t *testing.T) {
	sender := &recordingSender{done: make(chan struct{}, 1)}
	backend := &blockingAnalyzer{started: make(chan struct{})}
	engine := NewAnalyzer(recordingCapture{}, backend, sender, "prompt", false, "codex", nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = engine.Run(ctx) }()
	if err := engine.CaptureFrom(ctx, "123", "test"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-backend.started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for analyzer")
	}
	if !engine.Cancel() {
		t.Fatal("Cancel() = false, want true")
	}
	select {
	case <-sender.done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for cancellation result")
	}
	waitUntilIdle(t, engine)
}

func TestCaptureCanSelectResolverProfile(t *testing.T) {
	sender := &recordingSender{done: make(chan struct{}, 1)}
	backend := resultAnalyzer{provider: "codex", text: "profile result"}
	engine := NewAnalyzer(recordingCapture{}, resultAnalyzer{provider: "vision", text: "default"}, sender, "prompt", false, "vision", func(profile string) (analyzer.Analyzer, string, error) {
		if profile != "codex" {
			t.Fatalf("profile = %q", profile)
		}
		return backend, "codex", nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = engine.Run(ctx) }()
	if err := engine.CaptureFromProfile(ctx, "123", "telegram", "codex"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-sender.done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for profile result")
	}
	waitUntilIdle(t, engine)
	if status := engine.Status(); status.Backend != "codex" || status.Profile != "codex" {
		t.Fatalf("status = %+v", status)
	}
}

type resultAnalyzer struct {
	provider string
	text     string
}

func (a resultAnalyzer) Analyze(context.Context, analyzer.Request) (analyzer.Result, error) {
	return analyzer.Result{Provider: a.provider, Text: a.text}, nil
}

func waitUntilIdle(t *testing.T, engine *Engine) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for engine.Status().Busy && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if engine.Status().Busy {
		t.Fatal("engine is still busy")
	}
}
