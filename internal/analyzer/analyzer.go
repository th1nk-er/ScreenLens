package analyzer

import (
	"context"
	"time"

	"github.com/th1nk-er/ScreenLens/internal/vision"
)

// Request is the provider-neutral input to an analysis backend.
//
// Image is intentionally kept in memory at this boundary. Backends that need
// a file path (local CLI agents) are responsible for staging and cleaning up
// their own temporary artifact.
type Request struct {
	Image          []byte
	MIMEType       string
	Prompt         string
	Source         string
	PreviousOutput string
	StepIndex      int
	StepName       string
}

// Result contains the user-facing answer and provider metadata useful for
// diagnostics, status reporting, and future session-aware backends.
type Result struct {
	Text      string
	Provider  string
	SessionID string
	RunID     string
	ExitCode  int
	Duration  time.Duration
	Workflow  string
	StepIndex int
	StepName  string
	StepCount int
}

// Analyzer is the stable application boundary between the workflow and any
// analysis implementation: hosted vision APIs, local CLI agents, or future
// native transports can all implement it without changing the workflow.
type Analyzer interface {
	Analyze(context.Context, Request) (Result, error)
}

// VisionAdapter preserves compatibility with the existing protocol-oriented
// vision clients while exposing the new Analyzer contract.
type VisionAdapter struct {
	client   vision.Vision
	mimeType string
}

func NewVisionAdapter(client vision.Vision, mimeType string) *VisionAdapter {
	return &VisionAdapter{client: client, mimeType: mimeType}
}

func (a *VisionAdapter) Analyze(ctx context.Context, request Request) (Result, error) {
	if a == nil || a.client == nil {
		return Result{}, ErrUnavailable
	}
	text, err := a.client.Analyze(ctx, request.Image, request.Prompt)
	if err != nil {
		return Result{}, err
	}
	return Result{Text: text, Provider: "vision", Duration: 0}, nil
}

// ErrUnavailable is returned when the application is missing its selected
// analysis backend.
var ErrUnavailable = unavailableError{}

type unavailableError struct{}

func (unavailableError) Error() string { return "analysis backend is unavailable" }
