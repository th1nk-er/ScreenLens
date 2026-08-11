package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/th1nk-er/ScreenLens/internal/analyzer"
	"github.com/th1nk-er/ScreenLens/internal/artifact"
	"github.com/th1nk-er/ScreenLens/internal/config"
)

const (
	placeholderPrompt    = "{prompt}"
	placeholderImagePath = "{image_path}"
	placeholderWorkDir   = "{workdir}"
	placeholderSessionID = "{session_id}"
)

type Info struct {
	Profile  string
	Provider string
	Command  string
}

type CLI struct {
	profileName     string
	provider        string
	command         string
	args            []string
	builtInArgs     bool
	workDir         string
	env             map[string]string
	timeout         time.Duration
	maxOutput       int
	maxOutputTokens int
	retryCount      int
	persistent      bool
	runner          ProcessRunner
	prompt          string

	mu        sync.Mutex
	sessionID string
}

func Build(analysis config.AnalysisConfig, profiles map[string]config.LocalAgentProfile) (*CLI, Info, error) {
	return buildWithRunner(analysis, profiles, OSProcessRunner{})
}

func buildWithRunner(analysis config.AnalysisConfig, profiles map[string]config.LocalAgentProfile, runner ProcessRunner) (*CLI, Info, error) {
	if runner == nil {
		return nil, Info{}, fmt.Errorf("local agent process runner is nil")
	}
	profileName, profile, err := resolveProfile(analysis, profiles)
	if err != nil {
		return nil, Info{}, err
	}
	provider := normalizeProvider(profile.Provider)
	if provider == "" {
		provider = normalizeProvider(profileName)
	}
	if provider == "" || provider == config.LocalAgentProviderAuto {
		return nil, Info{}, fmt.Errorf("local agent profile %q must select a provider", profileName)
	}
	command := strings.TrimSpace(profile.Command)
	if command == "" {
		command = defaultCommand(provider)
	}
	if requiresCommandLookup(runner) {
		if _, err := exec.LookPath(command); err != nil {
			return nil, Info{}, fmt.Errorf("local agent %q is not available as %q; install it or set analysis.profiles.%s.local_agent.command: %w", provider, command, profileName, err)
		}
	}
	transport := strings.ToLower(strings.TrimSpace(profile.Transport))
	if transport == "" {
		transport = config.LocalAgentTransportCLI
	}
	if transport != config.LocalAgentTransportCLI {
		return nil, Info{}, fmt.Errorf("local agent profile %q uses unsupported transport %q", profileName, transport)
	}
	timeoutValue := analysis.Timeout
	if strings.TrimSpace(profile.Timeout) != "" {
		timeoutValue = profile.Timeout
	}
	timeout, err := time.ParseDuration(timeoutValue)
	if err != nil || timeout <= 0 {
		return nil, Info{}, fmt.Errorf("local agent profile %q timeout must be positive", profileName)
	}
	maxOutput := analysis.MaxOutputBytes
	if profile.MaxOutputBytes < 0 {
		return nil, Info{}, fmt.Errorf("local agent profile %q output limit must not be negative", profileName)
	}
	if profile.MaxOutputBytes > 0 {
		maxOutput = profile.MaxOutputBytes
	}
	if maxOutput < 1 {
		return nil, Info{}, fmt.Errorf("local agent profile %q output limit must be positive", profileName)
	}
	maxOutputTokens := profile.MaxOutputTokens
	if maxOutputTokens == 0 {
		maxOutputTokens = config.DefaultAgentMaxOutputTokens
	}
	if maxOutputTokens < 1 {
		return nil, Info{}, fmt.Errorf("local agent profile %q max output tokens must be positive", profileName)
	}
	retryCount := config.DefaultAgentRetryCount
	if profile.RetryCount != nil {
		retryCount = *profile.RetryCount
	}
	if retryCount < 0 {
		return nil, Info{}, fmt.Errorf("local agent profile %q retry count must not be negative", profileName)
	}
	session := profile.Session
	if session == "" {
		session = analysis.Session
	}
	persistent := strings.EqualFold(session, config.LocalAgentSessionPersistent)
	builtInArgs := len(profile.Args) == 0
	args := append([]string(nil), profile.Args...)
	if builtInArgs {
		args = defaultArgs(provider, persistent)
	}
	return &CLI{
		profileName:     profileName,
		provider:        provider,
		command:         command,
		args:            args,
		builtInArgs:     builtInArgs,
		workDir:         strings.TrimSpace(profile.WorkDir),
		env:             cloneEnv(profile.Env),
		timeout:         timeout,
		maxOutput:       maxOutput,
		maxOutputTokens: maxOutputTokens,
		retryCount:      retryCount,
		persistent:      persistent,
		runner:          runner,
		prompt:          analysis.Prompt,
	}, Info{Profile: profileName, Provider: provider, Command: command}, nil
}

func requiresCommandLookup(runner ProcessRunner) bool {
	switch runner.(type) {
	case OSProcessRunner, *OSProcessRunner:
		return true
	default:
		// Test and embedding runners own their command resolution. Requiring a
		// real provider binary here would make deterministic unit tests depend on
		// a user's local CLI installation.
		return false
	}
}

func (c *CLI) Analyze(ctx context.Context, request analyzer.Request) (analyzer.Result, error) {
	if c == nil {
		return analyzer.Result{}, analyzer.ErrUnavailable
	}
	if ctx == nil {
		ctx = context.Background()
	}
	completed := false
	defer func() {
		if c.persistent && !completed {
			// A failed resume can leave the stored session unusable. Do not
			// poison the next independent screenshot request with the same ID.
			c.ResetSession()
		}
	}()

	attempts := c.retryCount + 1
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		result, err := c.analyzeOnce(ctx, request)
		if err == nil {
			completed = true
			return result, nil
		}
		lastErr = err
		if c.persistent {
			c.ResetSession()
		}
		if attempt == attempts-1 || !retryable(err) {
			return analyzer.Result{}, diagnoseAgentError(c.provider, err)
		}
		if err := waitRetry(ctx, attempt); err != nil {
			return analyzer.Result{}, diagnoseAgentError(c.provider, err)
		}
	}
	return analyzer.Result{}, diagnoseAgentError(c.provider, lastErr)
}

func (c *CLI) analyzeOnce(ctx context.Context, request analyzer.Request) (analyzer.Result, error) {
	prompt := strings.TrimSpace(request.Prompt)
	if prompt == "" {
		prompt = c.prompt
	}
	if prompt == "" {
		return analyzer.Result{}, fmt.Errorf("local agent prompt is empty")
	}
	workDir, err := c.resolveWorkDir()
	if err != nil {
		return analyzer.Result{}, err
	}
	configuredWorkDir := workDir != ""
	image, err := artifact.StageInDir(request.Image, request.MIMEType, workDir)
	if err != nil {
		return analyzer.Result{}, err
	}
	defer func() {
		if cleanupErr := image.Cleanup(); cleanupErr != nil {
			slog.Warn("failed to clean local agent screenshot artifact", "path", image.Path, "error", cleanupErr)
		}
	}()
	if workDir == "" {
		workDir = image.Dir
	}
	processDir := image.Dir
	if configuredWorkDir {
		processDir = workDir
	}
	prompt = buildPrompt(prompt, image.Path, workDir)
	if c.provider == "claude" {
		prompt = attachClaudeImage(prompt, image.Path)
	}
	c.mu.Lock()
	sessionID := c.sessionID
	c.mu.Unlock()
	args := expandArgs(c.args, prompt, image.Path, workDir, sessionID)
	if c.persistent && c.builtInArgs && sessionID != "" {
		args = defaultArgsForSession(c.provider, prompt, image.Path, sessionID)
	}
	if c.provider == "codex" {
		args = ensureCodexAttachment(args, image.Path, prompt)
	}
	if c.provider == "opencode" {
		args = ensureOpenCodeAttachment(args, image.Path, prompt)
	}
	envAdditions := map[string]string{
		"SCREENLENS_IMAGE_PATH": image.Path,
		"SCREENLENS_SOURCE":     request.Source,
		"SCREENLENS_PROVIDER":   c.provider,
	}
	if c.provider == "opencode" {
		// The screen analyzer is intentionally read-only and non-interactive.
		// OpenCode documents OPENCODE_PERMISSION as the environment equivalent
		// of its permission config. The pure mode and disabled default plugins
		// prevent user-installed extensions from changing the execution surface.
		envAdditions["OPENCODE_PERMISSION"] = `{"*":"deny","read":"allow","glob":"allow","grep":"allow"}`
		envAdditions["OPENCODE_CONFIG_CONTENT"] = `{"mcp":{},"permission":{"*":"deny","read":"allow","glob":"allow","grep":"allow"}}`
		envAdditions["OPENCODE_DISABLE_DEFAULT_PLUGINS"] = "true"
	}
	if c.provider == "claude" {
		if _, configured := c.env["CLAUDE_CODE_MAX_OUTPUT_TOKENS"]; !configured {
			envAdditions["CLAUDE_CODE_MAX_OUTPUT_TOKENS"] = strconv.Itoa(c.maxOutputTokens)
		}
	}
	env := mergedEnv(c.env, envAdditions)
	callCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	started := time.Now()
	processResult, err := c.runner.Run(callCtx, ProcessSpec{
		Command:        c.command,
		Args:           args,
		Dir:            processDir,
		Env:            env,
		MaxOutputBytes: c.maxOutput,
	})
	if err != nil {
		diagnostic := strings.TrimSpace(string(processResult.Stderr))
		if diagnostic == "" {
			diagnostic = strings.TrimSpace(string(processResult.Stdout))
		}
		if diagnostic != "" {
			return analyzer.Result{}, fmt.Errorf("%w: %s", err, truncate(diagnostic))
		}
		return analyzer.Result{}, err
	}
	result, err := parseOutput(c.provider, processResult.Stdout)
	if err != nil {
		if len(processResult.Stderr) > 0 {
			return analyzer.Result{}, fmt.Errorf("%w: %s", err, truncate(string(processResult.Stderr)))
		}
		return analyzer.Result{}, err
	}
	result.ExitCode = processResult.ExitCode
	result.Duration = time.Since(started)
	if c.persistent && result.SessionID != "" {
		c.mu.Lock()
		c.sessionID = result.SessionID
		c.mu.Unlock()
	}
	return result, nil
}

func retryable(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrOutputLimit) {
		return false
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{
		"authentication", "unauthorized", "invalid api key", "api key", "login required",
		"permission denied", "access denied", "unknown option", "unrecognized option",
		"unknown flag", "invalid flag", "command not found", "not found",
		"mcp config", "invalid mcp", "file not found",
		"context length", "maximum context", "invalid request", "status code 400",
	} {
		if strings.Contains(message, marker) {
			return false
		}
	}
	return true
}

func waitRetry(ctx context.Context, attempt int) error {
	delay := time.Duration(attempt+1) * time.Second
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func diagnoseAgentError(provider string, err error) error {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "login required"), strings.Contains(message, "not authenticated"), strings.Contains(message, "unauthorized"), strings.Contains(message, "authentication"), strings.Contains(message, "invalid api key"):
		return fmt.Errorf("local agent %q authentication failed; sign in to the CLI or configure its credentials: %w", provider, err)
	case strings.Contains(message, "rate limit"), strings.Contains(message, "too many requests"), strings.Contains(message, "status code 429"):
		return fmt.Errorf("local agent %q was rate limited; retry later: %w", provider, err)
	case strings.Contains(message, "context length"), strings.Contains(message, "maximum context"), strings.Contains(message, "input_tokens"):
		return fmt.Errorf("local agent %q request exceeded the model context window; reduce prompt or output limits: %w", provider, err)
	case strings.Contains(message, "invalid request"), strings.Contains(message, "status code 400"):
		return fmt.Errorf("local agent %q rejected the request: %w", provider, err)
	case strings.Contains(message, "unknown option"), strings.Contains(message, "unrecognized option"), strings.Contains(message, "unknown flag"), strings.Contains(message, "invalid flag"):
		return fmt.Errorf("local agent %q CLI arguments are incompatible with the installed version: %w", provider, err)
	case strings.Contains(message, "trusted directory"), strings.Contains(message, "not inside a trusted"), strings.Contains(message, "git repository"):
		return fmt.Errorf("local agent %q rejected the working directory; verify its repository/trust settings: %w", provider, err)
	default:
		return fmt.Errorf("local agent %q failed: %w", provider, err)
	}
}

func (c *CLI) ResetSession() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.sessionID = ""
	c.mu.Unlock()
}

func (c *CLI) resolveWorkDir() (string, error) {
	value := strings.TrimSpace(c.workDir)
	if value == "" || strings.EqualFold(value, config.LocalAgentWorkDirTemp) {
		return "", nil
	}
	abs, err := filepath.Abs(value)
	if err != nil {
		return "", fmt.Errorf("resolve local agent work_dir: %w", err)
	}
	if err := os.MkdirAll(abs, 0o700); err != nil {
		return "", fmt.Errorf("create local agent work_dir: %w", err)
	}
	return abs, nil
}

func resolveProfile(analysis config.AnalysisConfig, profiles map[string]config.LocalAgentProfile) (string, config.LocalAgentProfile, error) {
	all := builtInProfiles()
	for name, profile := range profiles {
		all[strings.ToLower(strings.TrimSpace(name))] = profile
	}
	name := strings.ToLower(strings.TrimSpace(analysis.Profile))
	if name == "" {
		name = config.LocalAgentProviderAuto
	}
	if name == config.LocalAgentProviderAuto {
		for _, candidate := range []string{"codex", "claude", "opencode"} {
			profile := all[candidate]
			command := profile.Command
			if command == "" {
				command = defaultCommand(normalizeProvider(profile.Provider))
			}
			if _, err := exec.LookPath(command); err == nil {
				return candidate, profile, nil
			}
		}
		return "", config.LocalAgentProfile{}, fmt.Errorf("no supported local agent found; install Codex, Claude Code, or OpenCode, or configure analysis.profiles")
	}
	profile, ok := all[name]
	if !ok {
		return "", config.LocalAgentProfile{}, fmt.Errorf("local agent profile %q is not configured", name)
	}
	if profile.Provider == "" {
		profile.Provider = name
	}
	return name, profile, nil
}

func builtInProfiles() map[string]config.LocalAgentProfile {
	return map[string]config.LocalAgentProfile{
		"codex":    {Provider: "codex", Transport: config.LocalAgentTransportCLI},
		"claude":   {Provider: "claude", Transport: config.LocalAgentTransportCLI},
		"opencode": {Provider: "opencode", Transport: config.LocalAgentTransportCLI},
	}
}

func normalizeProvider(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "claude-code", "claude_code":
		return "claude"
	case "open-code", "open_code":
		return "opencode"
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func defaultCommand(provider string) string {
	switch provider {
	case "codex":
		return "codex"
	case "claude":
		return "claude"
	case "opencode":
		return "opencode"
	default:
		return provider
	}
}

func defaultArgs(provider string, persistent bool) []string {
	switch provider {
	case "codex":
		args := []string{"exec", "--json", "--sandbox", "read-only", "--skip-git-repo-check", "--ignore-user-config", "--image", placeholderImagePath}
		if !persistent {
			args = append(args, "--ephemeral")
		}
		return append(args, placeholderPrompt)
	case "claude":
		args := []string{"-p", placeholderPrompt, "--output-format", "json", "--no-session-persistence", "--permission-mode", "dontAsk", "--tools", "Read", "--allowed-tools", "Read", "--strict-mcp-config"}
		if persistent {
			args = []string{"-p", placeholderPrompt, "--output-format", "json", "--permission-mode", "dontAsk", "--tools", "Read", "--allowed-tools", "Read", "--strict-mcp-config"}
		}
		return args
	case "opencode":
		return []string{"--pure", "run", "--format", "json", placeholderPrompt, "--file", placeholderImagePath}
	default:
		return []string{placeholderPrompt}
	}
}

func defaultArgsForSession(provider, prompt, imagePath, sessionID string) []string {
	switch provider {
	case "codex":
		return []string{"exec", "--skip-git-repo-check", "--ignore-user-config", "resume", sessionID, "--json", "--sandbox", "read-only", "--image", imagePath, prompt}
	case "claude":
		return []string{"--resume", sessionID, "-p", prompt, "--output-format", "json", "--permission-mode", "dontAsk", "--tools", "Read", "--allowed-tools", "Read", "--strict-mcp-config"}
	case "opencode":
		return []string{"--pure", "run", "--session", sessionID, "--format", "json", prompt, "--file", imagePath}
	default:
		return []string{prompt}
	}
}

func buildPrompt(prompt, imagePath, workDir string) string {
	hasImage := strings.Contains(prompt, placeholderImagePath)
	prompt = strings.ReplaceAll(prompt, placeholderImagePath, imagePath)
	prompt = strings.ReplaceAll(prompt, placeholderWorkDir, workDir)
	if !hasImage {
		prompt += "\n\nThe screenshot is available at this local path: " + imagePath + "\nRead the image before answering. Do not modify files."
	}
	return prompt
}

func attachClaudeImage(prompt, imagePath string) string {
	mention := "@" + imagePath
	if strings.Contains(prompt, mention) {
		return prompt
	}
	return prompt + "\n\nAttach the screenshot to this request and analyze it before answering: " + mention
}

func expandArgs(args []string, prompt, imagePath, workDir, sessionID string) []string {
	result := make([]string, len(args))
	for index, arg := range args {
		arg = strings.ReplaceAll(arg, placeholderPrompt, prompt)
		arg = strings.ReplaceAll(arg, placeholderImagePath, imagePath)
		arg = strings.ReplaceAll(arg, placeholderWorkDir, workDir)
		arg = strings.ReplaceAll(arg, placeholderSessionID, sessionID)
		result[index] = arg
	}
	foundPrompt := false
	for _, arg := range args {
		if strings.Contains(arg, placeholderPrompt) {
			foundPrompt = true
			break
		}
	}
	if !foundPrompt {
		result = append(result, prompt)
	}
	return result
}

func ensureOpenCodeAttachment(args []string, imagePath, prompt string) []string {
	hasPure := false
	fileIndex := -1
	fileValue := imagePath
	removeCount := 0
	for index, arg := range args {
		if arg == "--pure" {
			hasPure = true
		}
		if arg == "--file" || arg == "-f" {
			fileIndex = index
			removeCount = 1
			if index+1 < len(args) && args[index+1] != prompt && !strings.HasPrefix(args[index+1], "-") {
				fileValue = args[index+1]
				removeCount = 2
			}
			break
		}
		if strings.HasPrefix(arg, "--file=") {
			fileIndex = index
			fileValue = strings.TrimPrefix(arg, "--file=")
			removeCount = 1
			break
		}
	}
	if !hasPure {
		args = append([]string{"--pure"}, args...)
		if fileIndex >= 0 {
			fileIndex++
		}
	}
	if fileIndex >= 0 {
		result := make([]string, 0, len(args))
		result = append(result, args[:fileIndex]...)
		result = append(result, args[fileIndex+removeCount:]...)
		if strings.TrimSpace(fileValue) == "" {
			fileValue = imagePath
		}
		return append(result, "--file", fileValue)
	}
	return append(args, "--file", imagePath)
}

func ensureCodexAttachment(args []string, imagePath, prompt string) []string {
	for index, arg := range args {
		if arg == "-i" || arg == "--image" || strings.HasPrefix(arg, "--image=") || strings.HasPrefix(arg, "-i=") {
			if arg == "--image=" || arg == "-i=" {
				result := make([]string, 0, len(args)+1)
				result = append(result, args[:index]...)
				result = append(result, strings.TrimSuffix(arg, "="), imagePath)
				result = append(result, args[index+1:]...)
				return result
			}
			if arg == "-i" || arg == "--image" {
				if index+1 >= len(args) || args[index+1] == prompt || strings.TrimSpace(args[index+1]) == "" || strings.HasPrefix(args[index+1], "-") {
					result := make([]string, 0, len(args)+1)
					result = append(result, args[:index+1]...)
					result = append(result, imagePath)
					return append(result, args[index+1:]...)
				}
			}
			return args
		}
	}
	return append(args, "--image", imagePath)
}

func cloneEnv(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

func mergedEnv(overrides, additions map[string]string) []string {
	values := make(map[string]string)
	for _, item := range os.Environ() {
		key, value, ok := strings.Cut(item, "=")
		if ok {
			values[key] = value
		}
	}
	for key, value := range overrides {
		values[key] = value
	}
	for key, value := range additions {
		if value != "" {
			values[key] = value
		}
	}
	result := make([]string, 0, len(values))
	for key, value := range values {
		result = append(result, key+"="+value)
	}
	return result
}

func truncate(value string) string {
	value = strings.TrimSpace(value)
	const maxLength = 1000
	if len(value) <= maxLength {
		return value
	}
	return value[:maxLength] + "..."
}
