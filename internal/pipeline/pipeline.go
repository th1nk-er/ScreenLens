package pipeline

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/th1nk-er/ScreenLens/internal/analyzer"
)

const previousOutputPlaceholder = "{previous_output}"

const (
	InputBoth       = "both"
	InputScreenshot = "screenshot"
	InputPrevious   = "previous"
)

// Step is the runtime form of one configured workflow step. Construction of
// the provider-specific Analyzer belongs to the application composition root;
// the pipeline only coordinates stable analyzer contracts.
type Step struct {
	Name     string
	Profile  string
	Prompt   string
	Input    string
	Analyzer analyzer.Analyzer
}

// Definition contains the immutable execution plan for one workflow. The
// timeout bounds the whole chain, while provider-specific timeouts continue to
// bound each individual analyzer call.
type Definition struct {
	Name    string
	Timeout time.Duration
	Steps   []Step
}

// Pipeline executes steps strictly in order. A step receives the original
// screenshot, the previous step's text, or both according to its Input mode.
// The previous text is also rendered into the prompt unless the prompt
// explicitly uses {previous_output}; this makes simple YAML definitions safe
// by default while still allowing precise prompt placement.
type Pipeline struct {
	name    string
	timeout time.Duration
	steps   []Step
}

func New(definition Definition) (*Pipeline, error) {
	name := strings.TrimSpace(definition.Name)
	if name == "" {
		return nil, errors.New("pipeline name is required")
	}
	if definition.Timeout <= 0 {
		return nil, errors.New("pipeline timeout must be positive")
	}
	if len(definition.Steps) == 0 {
		return nil, errors.New("pipeline must contain at least one step")
	}
	steps := make([]Step, len(definition.Steps))
	copy(steps, definition.Steps)
	for index := range steps {
		steps[index].Name = strings.TrimSpace(steps[index].Name)
		if steps[index].Name == "" {
			steps[index].Name = fmt.Sprintf("step-%d", index+1)
		}
		if strings.TrimSpace(steps[index].Prompt) == "" {
			return nil, fmt.Errorf("pipeline step %d prompt is empty", index+1)
		}
		steps[index].Input = strings.ToLower(strings.TrimSpace(steps[index].Input))
		if steps[index].Input == "" {
			steps[index].Input = InputBoth
		}
		switch steps[index].Input {
		case InputBoth, InputScreenshot:
		case InputPrevious:
			if index == 0 {
				return nil, fmt.Errorf("pipeline step %d cannot use previous input as its first input", index+1)
			}
		default:
			return nil, fmt.Errorf("pipeline step %d input must be %s, %s, or %s", index+1, InputBoth, InputScreenshot, InputPrevious)
		}
		if steps[index].Analyzer == nil {
			return nil, fmt.Errorf("pipeline step %d analyzer is unavailable", index+1)
		}
	}
	return &Pipeline{name: name, timeout: definition.Timeout, steps: steps}, nil
}

func (p *Pipeline) Name() string {
	if p == nil {
		return ""
	}
	return p.name
}

func (p *Pipeline) Steps() []Step {
	if p == nil {
		return nil
	}
	steps := make([]Step, len(p.steps))
	copy(steps, p.steps)
	return steps
}

func (p *Pipeline) Analyze(ctx context.Context, request analyzer.Request) (analyzer.Result, error) {
	if p == nil {
		return analyzer.Result{}, analyzer.ErrUnavailable
	}
	if ctx == nil {
		ctx = context.Background()
	}
	runCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	previous := ""
	var final analyzer.Result
	for index, step := range p.steps {
		if err := runCtx.Err(); err != nil {
			return analyzer.Result{}, fmt.Errorf("workflow %q stopped before step %q: %w", p.name, step.Name, err)
		}
		stepRequest := request
		includePrevious := index > 0 && step.Input != InputScreenshot
		if step.Input == InputPrevious {
			stepRequest.Image = nil
		}
		stepRequest.Prompt = renderPrompt(step.Prompt, previous, includePrevious)
		if includePrevious {
			stepRequest.PreviousOutput = previous
		} else {
			stepRequest.PreviousOutput = ""
		}
		stepRequest.StepIndex = index
		stepRequest.StepName = step.Name
		started := time.Now()
		slog.Info("analysis workflow step started",
			"workflow", p.name,
			"step", index+1,
			"step_name", step.Name,
			"profile", step.Profile,
			"input", step.Input,
			"image_bytes", len(stepRequest.Image),
			"previous_output_bytes", len(stepRequest.PreviousOutput),
		)
		result, err := step.Analyzer.Analyze(runCtx, stepRequest)
		if err != nil {
			slog.Error("analysis workflow step failed",
				"workflow", p.name,
				"step", index+1,
				"step_name", step.Name,
				"profile", step.Profile,
				"duration", time.Since(started),
				"error", err,
			)
			return analyzer.Result{}, fmt.Errorf("workflow %q step %d %q (profile %q): %w", p.name, index+1, step.Name, step.Profile, err)
		}
		if strings.TrimSpace(result.Text) == "" {
			slog.Error("analysis workflow step returned empty output",
				"workflow", p.name,
				"step", index+1,
				"step_name", step.Name,
				"profile", step.Profile,
				"duration", time.Since(started),
			)
			return analyzer.Result{}, fmt.Errorf("workflow %q step %d %q (profile %q) returned an empty result", p.name, index+1, step.Name, step.Profile)
		}
		result.Text = strings.TrimSpace(result.Text)
		result.Workflow = p.name
		result.StepIndex = index
		result.StepName = step.Name
		result.StepCount = len(p.steps)
		previous = result.Text
		final = result
		slog.Info("analysis workflow step completed",
			"workflow", p.name,
			"step", index+1,
			"step_name", step.Name,
			"profile", step.Profile,
			"duration", time.Since(started),
			"output_bytes", len(result.Text),
		)
	}
	return final, nil
}

func renderPrompt(template, previous string, includePrevious bool) string {
	if !includePrevious {
		return strings.TrimSpace(strings.ReplaceAll(template, previousOutputPlaceholder, ""))
	}
	prompt := strings.ReplaceAll(template, previousOutputPlaceholder, previous)
	if !strings.Contains(template, previousOutputPlaceholder) {
		prompt += "\n\nPrevious step output:\n" + previous
	}
	return strings.TrimSpace(prompt)
}
