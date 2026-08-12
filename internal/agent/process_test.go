package agent

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/th1nk-er/ScreenLens/internal/analyzer"
	"github.com/th1nk-er/ScreenLens/internal/config"
)

func TestHelperProcess(t *testing.T) {
	if os.Getenv("SCREENLENS_HELPER_PROCESS") != "1" {
		return
	}
	if os.Getenv("SCREENLENS_HELPER_MODE") == "spam" {
		_, _ = os.Stdout.WriteString(strings.Repeat("x", 1024))
		os.Exit(0)
	}
	if os.Getenv("SCREENLENS_HELPER_MODE") == "block" {
		time.Sleep(time.Hour)
	}
	_, _ = os.Stdout.WriteString("ok")
	os.Exit(0)
}

func TestOSProcessRunnerEnforcesOutputLimit(t *testing.T) {
	result, err := (OSProcessRunner{}).Run(context.Background(), ProcessSpec{
		Command:        os.Args[0],
		Args:           []string{"-test.run=TestHelperProcess"},
		Env:            append(os.Environ(), "SCREENLENS_HELPER_PROCESS=1", "SCREENLENS_HELPER_MODE=spam"),
		MaxOutputBytes: 32,
	})
	if !errors.Is(err, ErrOutputLimit) {
		t.Fatalf("error = %v, result = %+v, want ErrOutputLimit", err, result)
	}
}

func TestOSProcessRunnerHonorsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	result, err := (OSProcessRunner{}).Run(ctx, ProcessSpec{
		Command:        os.Args[0],
		Args:           []string{"-test.run=TestHelperProcess"},
		Env:            append(os.Environ(), "SCREENLENS_HELPER_PROCESS=1", "SCREENLENS_HELPER_MODE=block"),
		MaxOutputBytes: 1024,
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, result = %+v, want context deadline exceeded", err, result)
	}
}

func TestOSProcessRunner(t *testing.T) {
	result, err := (OSProcessRunner{}).Run(context.Background(), ProcessSpec{
		Command:        os.Args[0],
		Args:           []string{"-test.run=TestHelperProcess"},
		Env:            append(os.Environ(), "SCREENLENS_HELPER_PROCESS=1"),
		MaxOutputBytes: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(result.Stdout) != "ok" || result.ExitCode != 0 {
		t.Fatalf("unexpected process result: %+v", result)
	}
}

func TestBuildAndAnalyzeCleansArtifact(t *testing.T) {
	runner := &recordingRunner{stdout: `{"type":"item.completed","item":{"type":"agent_message","text":"answer"}}`}
	analysis := configForTest()
	cli, info, err := buildWithRunner(analysis, nil, runner)
	if err != nil {
		t.Fatal(err)
	}
	if info.Provider != "codex" {
		t.Fatalf("provider = %q", info.Provider)
	}
	result, err := cli.Analyze(context.Background(), analyzerRequestForTest())
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "answer" {
		t.Fatalf("result = %+v", result)
	}
	if runner.spec.Dir == "" || runner.imagePath == "" {
		t.Fatal("runner did not receive an artifact")
	}
	if _, err := os.Stat(runner.imagePath); !os.IsNotExist(err) {
		t.Fatalf("artifact was not cleaned up: %v", err)
	}
	if !strings.Contains(strings.Join(runner.spec.Args, " "), "Read the image") {
		t.Fatalf("prompt was not expanded: %v", runner.spec.Args)
	}
	if !containsArgument(runner.spec.Args, "--skip-git-repo-check") {
		t.Fatalf("Codex repository guard override is missing: %v", runner.spec.Args)
	}
	imageIndex := argumentIndex(runner.spec.Args, "--image")
	if imageIndex < 0 || imageIndex+1 >= len(runner.spec.Args) || !strings.Contains(runner.spec.Args[imageIndex+1], "screenshot.png") {
		t.Fatalf("Codex image attachment is missing: %v", runner.spec.Args)
	}
	promptIndex := -1
	for index, arg := range runner.spec.Args {
		if strings.Contains(arg, "Read the image") {
			promptIndex = index
			break
		}
	}
	if promptIndex < 0 || promptIndex >= imageIndex {
		t.Fatalf("Codex prompt must precede the variadic image attachment: %v", runner.spec.Args)
	}
}

func TestAnalyzeWithoutScreenshotUsesTextOnlyInvocation(t *testing.T) {
	runner := &recordingRunner{stdout: `{"type":"item.completed","item":{"type":"agent_message","text":"answer"}}`}
	cli, _, err := buildWithRunner(configForTest(), nil, runner)
	if err != nil {
		t.Fatal(err)
	}
	request := analyzerRequestForTest()
	request.Image = nil
	request.Prompt = "Answer based on the previous result."
	if _, err := cli.Analyze(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if containsArgument(runner.spec.Args, "--image") || runner.imagePath != "" {
		t.Fatalf("text-only request unexpectedly included image: args=%v path=%q", runner.spec.Args, runner.imagePath)
	}
	if strings.Contains(strings.Join(runner.spec.Args, " "), "Read the image") {
		t.Fatalf("text-only prompt references an image: %v", runner.spec.Args)
	}
}

func TestNativeImageProvidersDoNotAddPathFallbackToPrompt(t *testing.T) {
	for _, provider := range []string{"codex", "claude", "opencode"} {
		t.Run(provider, func(t *testing.T) {
			adapter := DefaultProviderRegistry().Lookup(provider)
			prompt := adapter.PreparePrompt("Inspect the screenshot.", `C:\temp\screenshot.jpg`, `C:\temp`, config.LocalAgentImageAuto)
			if strings.Contains(prompt, "The screenshot is available at this local path") {
				t.Fatalf("provider %q received a path fallback prompt: %q", provider, prompt)
			}
		})
	}
}

func TestUnknownImageProviderReceivesPathFallback(t *testing.T) {
	prompt := genericAdapter{name: "custom"}.PreparePrompt("Inspect the screenshot.", `C:\temp\screenshot.jpg`, `C:\temp`, config.LocalAgentImageAuto)
	if !strings.Contains(prompt, "The screenshot is available at this local path") {
		t.Fatalf("custom provider did not receive a path fallback prompt: %q", prompt)
	}
}

func TestCustomProviderCanDeclareNativeImageTransport(t *testing.T) {
	prompt := genericAdapter{name: "custom"}.PreparePrompt("Inspect the screenshot.", `C:\temp\screenshot.jpg`, `C:\temp`, config.LocalAgentImageNative)
	if strings.Contains(prompt, "The screenshot is available at this local path") {
		t.Fatalf("native custom provider received a path fallback prompt: %q", prompt)
	}
}

func TestInjectedProcessRunnerDoesNotRequireInstalledCLI(t *testing.T) {
	if requiresCommandLookup(&recordingRunner{}) {
		t.Fatal("injected process runner must not require a provider executable")
	}
	if !requiresCommandLookup(OSProcessRunner{}) {
		t.Fatal("OS process runner must validate the provider executable")
	}
}

func TestCustomProviderRegistryParticipatesInAutoDiscovery(t *testing.T) {
	analysis := configForTest()
	analysis.Profile = config.LocalAgentProviderAuto
	analysis.AutoProfiles = []string{"custom-agent"}
	registry := NewProviderRegistry(genericAdapter{name: "custom-agent"})
	cli, info, err := buildWithRunnerAndRegistry(analysis, nil, &recordingRunner{stdout: "answer"}, registry)
	if err != nil {
		t.Fatal(err)
	}
	if info.Provider != "custom-agent" || cli.provider != "custom-agent" {
		t.Fatalf("provider info = %+v, cli provider = %q", info, cli.provider)
	}
}

func TestBuildPrefersPowerShellCodexWrapperOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows command wrappers are only relevant on Windows")
	}
	if _, err := exec.LookPath("codex"); err != nil {
		t.Skip("Codex CLI is not installed")
	}
	cli, info, err := buildWithRunner(configForTest(), nil, OSProcessRunner{})
	if err != nil {
		t.Fatal(err)
	}
	if len(cli.commandPrefix) == 0 || !strings.Contains(strings.ToLower(info.Command), "powershell") {
		t.Fatalf("Codex command = %q, prefix = %v; want PowerShell wrapper", info.Command, cli.commandPrefix)
	}
}

func TestCodexCustomArgsReceiveScreenshotAsImageAttachment(t *testing.T) {
	runner := &recordingRunner{stdout: `{"type":"item.completed","item":{"type":"agent_message","text":"answer"}}`}
	analysis := configForTest()
	cli, _, err := buildWithRunner(analysis, map[string]config.LocalAgentProfile{
		"codex": {Provider: "codex", Command: os.Args[0], Args: []string{"exec", "--json", placeholderPrompt}},
	}, runner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cli.Analyze(context.Background(), analyzerRequestForTest()); err != nil {
		t.Fatal(err)
	}
	imageIndex := argumentIndex(runner.spec.Args, "--image")
	if imageIndex < 0 || imageIndex+1 >= len(runner.spec.Args) || !strings.Contains(runner.spec.Args[imageIndex+1], "screenshot.png") {
		t.Fatalf("Codex custom args did not receive image attachment: %v", runner.spec.Args)
	}
}

func TestCodexIncompleteImageFlagIsRepaired(t *testing.T) {
	runner := &recordingRunner{stdout: `{"type":"item.completed","item":{"type":"agent_message","text":"answer"}}`}
	analysis := configForTest()
	cli, _, err := buildWithRunner(analysis, map[string]config.LocalAgentProfile{
		"codex": {Provider: "codex", Command: os.Args[0], Args: []string{"exec", "--json", "--image", placeholderPrompt}},
	}, runner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cli.Analyze(context.Background(), analyzerRequestForTest()); err != nil {
		t.Fatal(err)
	}
	imageIndex := argumentIndex(runner.spec.Args, "--image")
	if imageIndex < 0 || imageIndex+1 >= len(runner.spec.Args) || !strings.Contains(runner.spec.Args[imageIndex+1], "screenshot.png") {
		t.Fatalf("Codex incomplete image flag was not repaired: %v", runner.spec.Args)
	}
}

func TestClaudeDisablesMCPWithoutInlineConfig(t *testing.T) {
	runner := &recordingRunner{stdout: `{"type":"result","result":"answer"}`}
	analysis := configForTest()
	analysis.Profile = "claude"
	cli, _, err := buildWithRunner(analysis, map[string]config.LocalAgentProfile{
		"claude": {Provider: "claude", Command: os.Args[0]},
	}, runner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cli.Analyze(context.Background(), analyzerRequestForTest()); err != nil {
		t.Fatal(err)
	}
	if !containsArgument(runner.spec.Args, "--strict-mcp-config") {
		t.Fatalf("Claude MCP isolation is missing: %v", runner.spec.Args)
	}
	if !containsArgument(runner.spec.Args, "--allowed-tools") || !containsArgument(runner.spec.Args, "Read") {
		t.Fatalf("Claude Read tool is not auto-approved: %v", runner.spec.Args)
	}
	if containsArgument(runner.spec.Args, "--mcp-config") {
		t.Fatalf("Claude must not receive an inline MCP config on Windows: %v", runner.spec.Args)
	}
	if !envContains(runner.spec.Env, "CLAUDE_CODE_MAX_OUTPUT_TOKENS=8192") {
		t.Fatalf("Claude output token budget is missing: %v", runner.spec.Env)
	}
	if !strings.Contains(strings.Join(runner.spec.Args, " "), "@"+runner.imagePath) {
		t.Fatalf("Claude image attachment is missing: %v", runner.spec.Args)
	}
}

func TestProcessFailureIncludesAvailableDiagnosticOutput(t *testing.T) {
	retryCount := 0
	runner := &failingRunner{err: errors.New("local agent exited with code 1"), stdout: []byte("diagnostic from stdout")}
	analysis := configForTest()
	cli, _, err := buildWithRunner(analysis, map[string]config.LocalAgentProfile{
		"codex": {Provider: "codex", Command: os.Args[0], RetryCount: &retryCount},
	}, runner)
	if err != nil {
		t.Fatal(err)
	}
	_, err = cli.Analyze(context.Background(), analyzerRequestForTest())
	if err == nil || !strings.Contains(err.Error(), "diagnostic from stdout") {
		t.Fatalf("error = %v, want process diagnostic", err)
	}
}

func TestUnavailableModelIsPermanentAndDiagnosedBeforeContextMarkers(t *testing.T) {
	err := errors.New(`{"api_error_status":404,"result":"The selected model may not exist or you may not have access to it.","usage":{"input_tokens":0}}`)
	if retryableProviderError(err) {
		t.Fatal("unavailable model error was marked retryable")
	}
	diagnosed := diagnoseProviderError("claude", err)
	if !strings.Contains(diagnosed.Error(), "selected model is unavailable") {
		t.Fatalf("diagnosed error = %v", diagnosed)
	}
	if strings.Contains(diagnosed.Error(), "context window") {
		t.Fatalf("unavailable model was misdiagnosed as context error: %v", diagnosed)
	}
}

func TestEndpointNotFoundIsNotDiagnosedAsUnavailableModel(t *testing.T) {
	err := errors.New(`{"api_error_status":404,"result":"The endpoint was not found."}`)
	diagnosed := diagnoseProviderError("claude", err)
	if strings.Contains(diagnosed.Error(), "selected model is unavailable") {
		t.Fatalf("endpoint error was misdiagnosed as model error: %v", diagnosed)
	}
}

func TestOpenCodeReceivesScreenshotAsFileAttachment(t *testing.T) {
	runner := &recordingRunner{stdout: `{"type":"text","part":{"text":"answer"}}`}
	analysis := configForTest()
	analysis.Profile = "opencode"
	cli, _, err := buildWithRunner(analysis, map[string]config.LocalAgentProfile{
		"opencode": {Provider: "opencode", Command: os.Args[0], Args: []string{"--file", placeholderImagePath, placeholderPrompt}},
	}, runner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cli.Analyze(context.Background(), analyzerRequestForTest()); err != nil {
		t.Fatal(err)
	}
	fileIndex := argumentIndex(runner.spec.Args, "--file")
	if fileIndex < 0 || fileIndex+1 >= len(runner.spec.Args) || !strings.Contains(runner.spec.Args[fileIndex+1], "screenshot.png") {
		t.Fatalf("OpenCode file attachment is missing: %v", runner.spec.Args)
	}
	promptIndex := -1
	for index, arg := range runner.spec.Args {
		if strings.Contains(arg, "Read the image") {
			promptIndex = index
			break
		}
	}
	if promptIndex < 0 || promptIndex >= fileIndex {
		t.Fatalf("OpenCode prompt must appear before --file: %v", runner.spec.Args)
	}
	if !containsArgument(runner.spec.Args, "--pure") {
		t.Fatalf("OpenCode pure mode is missing: %v", runner.spec.Args)
	}
	if !envContains(runner.spec.Env, `OPENCODE_PERMISSION={"*":"deny","read":"allow"}`) {
		t.Fatalf("OpenCode permission policy is missing: %v", runner.spec.Env)
	}
	if envContains(runner.spec.Env, `OPENCODE_PERMISSION={"*":"deny","read":"allow","glob":"allow","grep":"allow"}`) {
		t.Fatal("OpenCode workspace-discovery permissions must remain disabled by default")
	}
}

func TestOpenCodeEnvironmentCanBeOverriddenByProfile(t *testing.T) {
	runner := &recordingRunner{stdout: `{"type":"text","part":{"text":"answer"}}`}
	analysis := configForTest()
	analysis.Profile = "opencode"
	cli, _, err := buildWithRunner(analysis, map[string]config.LocalAgentProfile{
		"opencode": {
			Provider: "opencode",
			Command:  os.Args[0],
			Env: map[string]string{
				"OPENCODE_PERMISSION": "custom-policy",
			},
		},
	}, runner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cli.Analyze(context.Background(), analyzerRequestForTest()); err != nil {
		t.Fatal(err)
	}
	if !envContains(runner.spec.Env, "OPENCODE_PERMISSION=custom-policy") {
		t.Fatalf("configured OpenCode permission policy was not preserved: %v", runner.spec.Env)
	}
}

func TestAnalyzeRetriesTransientFailure(t *testing.T) {
	runner := &flakyRunner{remainingFailures: 1, stdout: `{"type":"item.completed","item":{"type":"agent_message","text":"answer"}}`}
	analysis := configForTest()
	retryCount := 1
	cli, _, err := buildWithRunner(analysis, map[string]config.LocalAgentProfile{
		"codex": {Provider: "codex", Command: os.Args[0], RetryCount: &retryCount},
	}, runner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cli.Analyze(context.Background(), analyzerRequestForTest()); err != nil {
		t.Fatal(err)
	}
	if runner.calls != 2 {
		t.Fatalf("agent calls = %d, want 2", runner.calls)
	}
}

func TestPersistentSessionIsResetAfterFailure(t *testing.T) {
	runner := &failingRunner{err: errors.New("session is invalid")}
	analysis := configForTest()
	analysis.Session = config.LocalAgentSessionPersistent
	cli, _, err := buildWithRunner(analysis, map[string]config.LocalAgentProfile{
		"codex": {Provider: "codex", Command: os.Args[0], Session: config.LocalAgentSessionPersistent},
	}, runner)
	if err != nil {
		t.Fatal(err)
	}
	cli.sessionID = "stale-session"
	if _, err := cli.Analyze(context.Background(), analyzerRequestForTest()); err == nil {
		t.Fatal("expected persistent session failure")
	}
	if cli.sessionID != "" {
		t.Fatalf("session ID = %q, want empty after failure", cli.sessionID)
	}
}

func TestCodexPersistentResumeReceivesScreenshotAsImageAttachment(t *testing.T) {
	runner := &recordingRunner{stdout: `{"type":"item.completed","item":{"type":"agent_message","text":"answer"}}`}
	analysis := configForTest()
	analysis.Session = config.LocalAgentSessionPersistent
	cli, _, err := buildWithRunner(analysis, nil, runner)
	if err != nil {
		t.Fatal(err)
	}
	cli.sessionID = "session-1"
	if _, err := cli.Analyze(context.Background(), analyzerRequestForTest()); err != nil {
		t.Fatal(err)
	}
	imageIndex := argumentIndex(runner.spec.Args, "--image")
	if imageIndex < 0 || imageIndex+1 >= len(runner.spec.Args) || !strings.Contains(runner.spec.Args[imageIndex+1], "screenshot.png") {
		t.Fatalf("Codex resume did not receive image attachment: %v", runner.spec.Args)
	}
	promptIndex := -1
	for index, arg := range runner.spec.Args {
		if strings.Contains(arg, "Read the image") {
			promptIndex = index
			break
		}
	}
	if promptIndex < 0 || promptIndex >= imageIndex {
		t.Fatalf("Codex resume prompt must precede the variadic image attachment: %v", runner.spec.Args)
	}
}

func containsArgument(args []string, wanted string) bool {
	for _, arg := range args {
		if arg == wanted {
			return true
		}
	}
	return false
}

func argumentIndex(args []string, wanted string) int {
	for index, arg := range args {
		if arg == wanted {
			return index
		}
	}
	return -1
}

func envContains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

type recordingRunner struct {
	spec      ProcessSpec
	stdout    string
	imagePath string
}

type flakyRunner struct {
	remainingFailures int
	calls             int
	stdout            string
}

func (r *flakyRunner) Run(_ context.Context, spec ProcessSpec) (ProcessResult, error) {
	r.calls++
	if r.remainingFailures > 0 {
		r.remainingFailures--
		return ProcessResult{ExitCode: 1, Stderr: []byte("temporary failure")}, errors.New("local agent exited with code 1")
	}
	return ProcessResult{Stdout: []byte(r.stdout), ExitCode: 0}, nil
}

type failingRunner struct {
	err    error
	stdout []byte
	stderr []byte
}

func (r *failingRunner) Run(context.Context, ProcessSpec) (ProcessResult, error) {
	return ProcessResult{ExitCode: 1, Stdout: r.stdout, Stderr: r.stderr}, r.err
}

func (r *recordingRunner) Run(_ context.Context, spec ProcessSpec) (ProcessResult, error) {
	r.spec = spec
	for _, value := range spec.Env {
		if strings.HasPrefix(value, "SCREENLENS_IMAGE_PATH=") {
			r.imagePath = strings.TrimPrefix(value, "SCREENLENS_IMAGE_PATH=")
		}
	}
	if r.imagePath != "" {
		if _, err := os.Stat(r.imagePath); err != nil {
			return ProcessResult{}, err
		}
	}
	return ProcessResult{Stdout: []byte(r.stdout), ExitCode: 0}, nil
}

func configForTest() config.AnalysisConfig {
	return config.AnalysisConfig{
		Mode:           config.AnalysisModeLocalAgent,
		Profile:        "codex",
		Prompt:         "Read the image and answer.",
		Timeout:        "1s",
		MaxOutputBytes: 1024,
		Session:        config.LocalAgentSessionEphemeral,
	}
}

func analyzerRequestForTest() analyzer.Request {
	return analyzer.Request{Image: []byte("image"), MIMEType: "image/png", Prompt: "Read the image and answer."}
}
