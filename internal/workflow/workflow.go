package workflow

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

const CaptureRequest = "capture.request"

var ErrCaptureInProgress = errors.New("a capture is already queued or in progress")

type Event struct {
	Name string
	Data any
}

type CaptureEvent struct {
	Target string
	Source string
}

type Capture interface {
	Screenshot() ([]byte, error)
}

type Vision interface {
	Analyze(ctx context.Context, image []byte, prompt string) (string, error)
}

type Sender interface {
	SendText(ctx context.Context, target, text string) error
	SendPhoto(ctx context.Context, target string, image []byte, caption string) (int, error)
	SendReply(ctx context.Context, target, text string, replyTo int) error
}

type Engine struct {
	mu        sync.RWMutex
	capture   Capture
	vision    Vision
	sender    Sender
	prompt    string
	sendImage bool

	events chan Event
	busy   bool
	queued bool
	status Status
}

type Status struct {
	Busy         bool
	LastStarted  time.Time
	LastFinished time.Time
	LastDuration time.Duration
	LastError    string
	LastWarning  string
}

func New(capture Capture, vision Vision, sender Sender, prompt string, sendImage bool) *Engine {
	return &Engine{
		capture:   capture,
		vision:    vision,
		sender:    sender,
		prompt:    prompt,
		sendImage: sendImage,
		events:    make(chan Event, 8),
	}
}

func (e *Engine) Publish(ctx context.Context, event Event) error {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case e.events <- event:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	default:
		return fmt.Errorf("workflow event queue is full")
	}
}

func (e *Engine) Capture(ctx context.Context, target string) error {
	return e.CaptureFrom(ctx, target, "request")
}

func (e *Engine) CaptureFrom(ctx context.Context, target, source string) error {
	e.mu.Lock()
	if e.busy || e.queued {
		e.mu.Unlock()
		return ErrCaptureInProgress
	}
	e.queued = true
	e.mu.Unlock()

	err := e.Publish(ctx, Event{Name: CaptureRequest, Data: CaptureEvent{Target: target, Source: source}})
	if err != nil {
		e.mu.Lock()
		e.queued = false
		e.mu.Unlock()
	}
	return err
}

func (e *Engine) Run(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case event := <-e.events:
			switch event.Name {
			case CaptureRequest:
				captureEvent, ok := event.Data.(CaptureEvent)
				if !ok {
					captureEvent = CaptureEvent{}
				}
				e.handleCapture(ctx, captureEvent.Target, captureEvent.Source)
			}
		}
	}
}

func (e *Engine) Replace(capture Capture, vision Vision, prompt string, sendImage bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.capture = capture
	e.vision = vision
	e.prompt = prompt
	e.sendImage = sendImage
}

func (e *Engine) Status() Status {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.status
}

func (e *Engine) handleCapture(ctx context.Context, target, source string) {
	if source == "" {
		source = "unknown"
	}
	slog.Info("capture started", "source", source)
	e.mu.Lock()
	if e.busy {
		e.mu.Unlock()
		slog.Warn("capture skipped because another capture is in progress", "source", source)
		_ = e.senderText(ctx, target, "A capture is already in progress; please wait.")
		return
	}
	e.busy = true
	e.queued = false
	e.status.Busy = true
	e.status.LastStarted = time.Now()
	e.status.LastWarning = ""
	capture, vision, prompt, sendImage := e.capture, e.vision, e.prompt, e.sendImage
	e.mu.Unlock()

	started := time.Now()
	defer func() {
		e.mu.Lock()
		e.busy = false
		e.status.Busy = false
		e.status.LastFinished = time.Now()
		e.status.LastDuration = time.Since(started)
		e.mu.Unlock()
	}()

	if capture == nil || vision == nil || e.sender == nil {
		e.fail(ctx, target, fmt.Errorf("workflow is not fully initialized"), 0)
		return
	}
	image, err := capture.Screenshot()
	if err != nil {
		e.fail(ctx, target, fmt.Errorf("capture screenshot: %w", err), 0)
		slog.Error("screenshot failed", "source", source, "error", err)
		return
	}
	slog.Info("screenshot captured", "source", source, "bytes", len(image))
	var screenshotMessageID int
	var screenshotWarning error
	if sendImage {
		// Deliver the screenshot as soon as capture succeeds. The LLM request
		// happens afterwards, so Telegram receives visual feedback immediately.
		screenshotMessageID, err = e.sender.SendPhoto(ctx, target, image, "ScreenLens capture")
		if err != nil {
			screenshotWarning = fmt.Errorf("send screenshot: %w", err)
			e.setLastWarning(screenshotWarning)
			slog.Error("screenshot delivery failed", "source", source, "error", err)
		} else {
			e.setLastWarning(nil)
			slog.Info("screenshot delivered", "source", source)
		}
	}
	result, err := vision.Analyze(ctx, image, prompt)
	if err != nil {
		e.fail(ctx, target, fmt.Errorf("analyze screenshot: %w", err), screenshotMessageID)
		slog.Error("screenshot analysis failed", "source", source, "error", err)
		return
	}
	if strings.TrimSpace(result) == "" {
		e.fail(ctx, target, fmt.Errorf("vision provider returned an empty result"), screenshotMessageID)
		return
	}

	if err := e.sendResult(ctx, target, result, screenshotMessageID); err != nil {
		e.fail(ctx, target, fmt.Errorf("send analysis result: %w", err), screenshotMessageID)
		slog.Error("analysis result delivery failed", "source", source, "error", err)
		return
	}
	e.setLastError(nil)
	if screenshotWarning != nil {
		slog.Warn("capture workflow completed with warning", "source", source, "warning", screenshotWarning)
	}
	slog.Info("capture workflow completed", "source", source)
}

func (e *Engine) sendResult(ctx context.Context, target, text string, replyTo int) error {
	if replyTo > 0 {
		return e.sender.SendReply(ctx, target, text, replyTo)
	}
	return e.sender.SendText(ctx, target, text)
}

func (e *Engine) fail(ctx context.Context, target string, err error, replyTo int) {
	e.setLastError(err)
	_ = e.sendResult(ctx, target, "ScreenLens error: "+err.Error(), replyTo)
}

func (e *Engine) senderText(ctx context.Context, target, text string) error {
	if e.sender == nil {
		return nil
	}
	return e.sender.SendText(ctx, target, text)
}

func (e *Engine) setLastError(err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err == nil {
		e.status.LastError = ""
		return
	}
	e.status.LastError = err.Error()
}

func (e *Engine) setLastWarning(err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err == nil {
		e.status.LastWarning = ""
		return
	}
	e.status.LastWarning = err.Error()
}
