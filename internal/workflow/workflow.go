package workflow

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/th1nk-er/ScreenLens/internal/analyzer"
)

const (
	CaptureRequest = "capture.request"

	CaptureSourceRequest  = "request"
	CaptureSourceHotkey   = "hotkey"
	CaptureSourceTelegram = "telegram"
	CaptureSourceTray     = "tray"
)

const (
	defaultCaptureSource = "unknown"
	eventQueueSize       = 8
	noReplyMessageID     = 0
	screenshotCaption    = "ScreenLens capture"
	errorMessagePrefix   = "ScreenLens error: "
	errorDeliveryTimeout = 10 * time.Second
)

var ErrCaptureInProgress = errors.New("a capture is already queued or in progress")

type Event struct {
	Name string
	Data any
}

type CaptureEvent struct {
	Target  string
	Source  string
	Profile string
}

type Capture interface {
	Screenshot() ([]byte, error)
}

type Vision interface {
	Analyze(ctx context.Context, image []byte, prompt string) (string, error)
}

// AnalyzerResolver allows a request to select a configured local-agent
// profile without making the workflow depend on a concrete provider package.
type AnalyzerResolver func(profile string) (analyzer.Analyzer, string, error)

type Sender interface {
	SendText(ctx context.Context, target, text string) error
	SendPhoto(ctx context.Context, target string, image []byte, caption string) (int, error)
	SendReply(ctx context.Context, target, text string, replyTo int) error
}

type Engine struct {
	mu        sync.RWMutex
	capture   Capture
	analyzer  analyzer.Analyzer
	sender    Sender
	prompt    string
	sendImage bool
	resolver  AnalyzerResolver
	backend   string
	cancel    context.CancelFunc

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
	Backend      string
	Profile      string
	SessionID    string
	ExitCode     int
}

func New(capture Capture, vision Vision, sender Sender, prompt string, sendImage bool) *Engine {
	return newEngine(capture, legacyAdapter{vision: vision}, sender, prompt, sendImage, "vision", nil)
}

func NewAnalyzer(capture Capture, backend analyzer.Analyzer, sender Sender, prompt string, sendImage bool, backendName string, resolver AnalyzerResolver) *Engine {
	if backendName == "" {
		backendName = "analyzer"
	}
	return newEngine(capture, backend, sender, prompt, sendImage, backendName, resolver)
}

func newEngine(capture Capture, backend analyzer.Analyzer, sender Sender, prompt string, sendImage bool, backendName string, resolver AnalyzerResolver) *Engine {
	if backendName == "" {
		backendName = "analyzer"
	}
	return &Engine{
		capture:   capture,
		analyzer:  backend,
		sender:    sender,
		prompt:    prompt,
		sendImage: sendImage,
		backend:   backendName,
		resolver:  resolver,
		events:    make(chan Event, eventQueueSize),
		status:    Status{Backend: backendName},
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
	return e.CaptureFrom(ctx, target, CaptureSourceRequest)
}

func (e *Engine) CaptureFrom(ctx context.Context, target, source string) error {
	return e.CaptureFromProfile(ctx, target, source, "")
}

func (e *Engine) CaptureFromProfile(ctx context.Context, target, source, profile string) error {
	e.mu.Lock()
	if e.busy || e.queued {
		e.mu.Unlock()
		return ErrCaptureInProgress
	}
	e.queued = true
	e.mu.Unlock()

	err := e.Publish(ctx, Event{Name: CaptureRequest, Data: CaptureEvent{Target: target, Source: source, Profile: profile}})
	if err != nil {
		e.mu.Lock()
		e.queued = false
		e.mu.Unlock()
	}
	return err
}

func (e *Engine) Run(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	defer func() {
		e.mu.Lock()
		e.queued = false
		e.mu.Unlock()
		for {
			select {
			case <-e.events:
			default:
				return
			}
		}
	}()
	for {
		// Prefer shutdown over a simultaneously-ready queued event. This keeps
		// a canceled engine from starting work that was waiting in the queue.
		select {
		case <-ctx.Done():
			return nil
		default:
		}
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
				e.handleCaptureProfile(ctx, captureEvent.Target, captureEvent.Source, captureEvent.Profile)
			}
		}
	}
}

func (e *Engine) Replace(capture Capture, vision Vision, prompt string, sendImage bool) {
	e.ReplaceAnalyzer(capture, legacyAdapter{vision: vision}, prompt, sendImage, "vision", nil)
}

func (e *Engine) ReplaceAnalyzer(capture Capture, backend analyzer.Analyzer, prompt string, sendImage bool, backendName string, resolver AnalyzerResolver) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.capture = capture
	e.analyzer = backend
	e.prompt = prompt
	e.sendImage = sendImage
	e.backend = backendName
	e.resolver = resolver
}

func (e *Engine) Cancel() bool {
	e.mu.RLock()
	cancel := e.cancel
	e.mu.RUnlock()
	if cancel == nil {
		return false
	}
	cancel()
	return true
}

func (e *Engine) Status() Status {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.status
}

func (e *Engine) handleCapture(ctx context.Context, target, source string) {
	e.handleCaptureProfile(ctx, target, source, "")
}

func (e *Engine) handleCaptureProfile(ctx context.Context, target, source, profile string) {
	if source == "" {
		source = defaultCaptureSource
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
	started := e.status.LastStarted
	e.status.LastWarning = ""
	e.status.SessionID = ""
	e.status.Profile = profile
	capture, backend, prompt, sendImage, resolver := e.capture, e.analyzer, e.prompt, e.sendImage, e.resolver
	backendName := e.backend
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		e.busy = false
		e.status.Busy = false
		e.status.LastFinished = time.Now()
		e.status.LastDuration = time.Since(started)
		e.mu.Unlock()
	}()
	if profile != "" {
		if resolver == nil {
			e.fail(ctx, target, fmt.Errorf("analysis profile %q is not available", profile), noReplyMessageID)
			return
		}
		resolved, resolvedName, err := resolver(profile)
		if err != nil {
			e.fail(ctx, target, fmt.Errorf("resolve analysis profile %q: %w", profile, err), noReplyMessageID)
			return
		}
		backend, backendName = resolved, resolvedName
	}
	if backendName == "" {
		backendName = "analyzer"
	}
	runCtx, cancel := context.WithCancel(ctx)
	e.mu.Lock()
	e.cancel = cancel
	e.status.Backend = backendName
	e.status.Profile = profile
	e.mu.Unlock()
	defer func() {
		cancel()
		e.mu.Lock()
		e.cancel = nil
		e.mu.Unlock()
	}()

	if capture == nil || backend == nil || e.sender == nil {
		e.fail(runCtx, target, fmt.Errorf("workflow is not fully initialized"), noReplyMessageID)
		return
	}
	image, err := capture.Screenshot()
	if err != nil {
		e.fail(ctx, target, fmt.Errorf("capture screenshot: %w", err), noReplyMessageID)
		slog.Error("screenshot failed", "source", source, "error", err)
		return
	}
	slog.Info("screenshot captured", "source", source, "bytes", len(image))
	var screenshotMessageID int
	var screenshotWarning error
	if sendImage {
		// Deliver the screenshot as soon as capture succeeds. The LLM request
		// happens afterwards, so Telegram receives visual feedback immediately.
		screenshotMessageID, err = e.sender.SendPhoto(runCtx, target, image, screenshotCaption)
		if err != nil {
			screenshotWarning = fmt.Errorf("send screenshot: %w", err)
			e.setLastWarning(screenshotWarning)
			slog.Error("screenshot delivery failed", "source", source, "error", err)
		} else {
			e.setLastWarning(nil)
			slog.Info("screenshot delivered", "source", source)
		}
	}
	result, err := backend.Analyze(runCtx, analyzer.Request{
		Image: image, MIMEType: captureMIMEType(capture), Prompt: prompt, Source: source,
	})
	if err != nil {
		// Keep the parent workflow context for error delivery. runCtx is
		// cancelled by a timeout or /cancel, so using it here would prevent the
		// user-facing failure message from being sent.
		e.fail(ctx, target, fmt.Errorf("analyze screenshot: %w", normalizeAnalysisError(err)), screenshotMessageID)
		slog.Error("screenshot analysis failed", "source", source, "error", err)
		return
	}
	if strings.TrimSpace(result.Text) == "" {
		e.fail(ctx, target, fmt.Errorf("vision provider returned an empty result"), screenshotMessageID)
		return
	}

	e.setAnalysisStatus(result)
	if err := e.sendResult(runCtx, target, result.Text, screenshotMessageID); err != nil {
		e.fail(runCtx, target, fmt.Errorf("send analysis result: %w", err), screenshotMessageID)
		slog.Error("analysis result delivery failed", "source", source, "error", err)
		return
	}
	e.setLastError(nil)
	if screenshotWarning != nil {
		slog.Warn("capture workflow completed with warning", "source", source, "warning", screenshotWarning)
	}
	slog.Info("capture workflow completed", "source", source)
}

func captureMIMEType(capture Capture) string {
	if typed, ok := capture.(interface{ MIMEType() string }); ok && typed.MIMEType() != "" {
		return typed.MIMEType()
	}
	return "image/jpeg"
}

func (e *Engine) setAnalysisStatus(result analyzer.Result) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if result.Provider != "" {
		e.status.Backend = result.Provider
	}
	e.status.SessionID = result.SessionID
	e.status.ExitCode = result.ExitCode
}

type legacyAdapter struct{ vision Vision }

func (a legacyAdapter) Analyze(ctx context.Context, request analyzer.Request) (analyzer.Result, error) {
	if a.vision == nil {
		return analyzer.Result{}, analyzer.ErrUnavailable
	}
	text, err := a.vision.Analyze(ctx, request.Image, request.Prompt)
	return analyzer.Result{Text: text, Provider: "vision"}, err
}

func (e *Engine) sendResult(ctx context.Context, target, text string, replyTo int) error {
	if e.sender == nil {
		return errors.New("workflow sender is unavailable")
	}
	if replyTo > noReplyMessageID {
		return e.sender.SendReply(ctx, target, text, replyTo)
	}
	return e.sender.SendText(ctx, target, text)
}

func (e *Engine) fail(ctx context.Context, target string, err error, replyTo int) {
	e.setLastError(err)
	sendCtx := ctx
	var cancel context.CancelFunc
	if sendCtx == nil || sendCtx.Err() != nil {
		sendCtx, cancel = context.WithTimeout(context.Background(), errorDeliveryTimeout)
		defer cancel()
	}
	if sendErr := e.sendResult(sendCtx, target, errorMessagePrefix+err.Error(), replyTo); sendErr != nil {
		slog.Warn("failed to deliver workflow error", "target", target, "error", sendErr)
	}
}

func normalizeAnalysisError(err error) error {
	switch {
	case errors.Is(err, context.Canceled):
		return fmt.Errorf("local analysis canceled: %w", err)
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("local analysis timed out: %w", err)
	default:
		return err
	}
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
