package pipeline

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/th1nk-er/ScreenLens/internal/analyzer"
)

type recordingAnalyzer struct {
	provider string
	output   string
	prompts  *[]string
	inputs   *[]string
	images   *[]bool
	err      error
}

type blockingAnalyzer struct{}

func (blockingAnalyzer) Analyze(ctx context.Context, _ analyzer.Request) (analyzer.Result, error) {
	<-ctx.Done()
	return analyzer.Result{}, ctx.Err()
}

func (a recordingAnalyzer) Analyze(_ context.Context, request analyzer.Request) (analyzer.Result, error) {
	if a.prompts != nil {
		*a.prompts = append(*a.prompts, request.Prompt)
	}
	if a.inputs != nil {
		*a.inputs = append(*a.inputs, request.PreviousOutput)
	}
	if a.images != nil {
		*a.images = append(*a.images, len(request.Image) > 0)
	}
	if a.err != nil {
		return analyzer.Result{}, a.err
	}
	return analyzer.Result{Provider: a.provider, Text: a.output}, nil
}

func TestPipelineRunsStepsInOrderAndPassesPreviousOutput(t *testing.T) {
	var prompts []string
	var inputs []string
	p, err := New(Definition{
		Name:    "screen-review",
		Timeout: time.Second,
		Steps: []Step{
			{Name: "inspect", Profile: "codex", Prompt: "Inspect the screenshot.", Analyzer: recordingAnalyzer{provider: "codex", output: "visible issue", prompts: &prompts, inputs: &inputs}},
			{Name: "summarize", Profile: "claude", Prompt: "Summarize this: {previous_output}", Analyzer: recordingAnalyzer{provider: "claude", output: "solution", prompts: &prompts, inputs: &inputs}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := p.Analyze(context.Background(), analyzer.Request{Image: []byte("image"), MIMEType: "image/jpeg", Source: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "solution" || result.Provider != "claude" {
		t.Fatalf("result = %+v", result)
	}
	if result.Workflow != "screen-review" || result.StepIndex != 1 || result.StepName != "summarize" || result.StepCount != 2 {
		t.Fatalf("step metadata = %+v", result)
	}
	if len(inputs) != 2 || inputs[0] != "" || inputs[1] != "visible issue" {
		t.Fatalf("previous outputs = %q", inputs)
	}
	if len(prompts) != 2 || !strings.Contains(prompts[1], "visible issue") {
		t.Fatalf("prompts = %q", prompts)
	}
}

func TestPipelineAppendsPreviousOutputWhenPromptHasNoPlaceholder(t *testing.T) {
	var prompts []string
	p, err := New(Definition{
		Name:    "chain",
		Timeout: time.Second,
		Steps: []Step{
			{Name: "one", Prompt: "one", Analyzer: recordingAnalyzer{output: "first"}},
			{Name: "two", Prompt: "two", Analyzer: recordingAnalyzer{output: "second", prompts: &prompts}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Analyze(context.Background(), analyzer.Request{}); err != nil {
		t.Fatal(err)
	}
	if len(prompts) != 1 || !strings.Contains(prompts[0], "Previous step output:\nfirst") {
		t.Fatalf("prompt = %q", prompts)
	}
}

func TestPipelinePreviousInputOmitsScreenshot(t *testing.T) {
	var images []bool
	p, err := New(Definition{
		Name:    "text-chain",
		Timeout: time.Second,
		Steps: []Step{
			{Name: "inspect", Prompt: "inspect", Input: InputScreenshot, Analyzer: recordingAnalyzer{output: "first"}},
			{Name: "reason", Prompt: "reason", Input: InputPrevious, Analyzer: recordingAnalyzer{output: "second", images: &images}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Analyze(context.Background(), analyzer.Request{Image: []byte("screen")}); err != nil {
		t.Fatal(err)
	}
	if len(images) != 1 || images[0] {
		t.Fatalf("second step image presence = %v, want false", images)
	}
}

func TestPipelineWrapsStepErrors(t *testing.T) {
	wantErr := errors.New("provider failed")
	p, err := New(Definition{
		Name:    "chain",
		Timeout: time.Second,
		Steps:   []Step{{Name: "reason", Profile: "deepseek", Prompt: "reason", Analyzer: recordingAnalyzer{err: wantErr}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Analyze(context.Background(), analyzer.Request{})
	if !errors.Is(err, wantErr) || !strings.Contains(err.Error(), `step 1 "reason"`) {
		t.Fatalf("error = %v", err)
	}
}

func TestPipelineTimeoutStopsChain(t *testing.T) {
	p, err := New(Definition{
		Name:    "chain",
		Timeout: time.Millisecond,
		Steps:   []Step{{Name: "one", Prompt: "one", Analyzer: blockingAnalyzer{}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Analyze(context.Background(), analyzer.Request{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want deadline exceeded", err)
	}
}
