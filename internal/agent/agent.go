package agent

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
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
	adapter         ProviderAdapter
	imageTransport  string
	command         string
	commandPrefix   []string
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
	return BuildWithRegistry(analysis, profiles, DefaultProviderRegistry())
}

// BuildWithRegistry allows an embedding application to add provider adapters
// without modifying the core process lifecycle or the built-in registry.
func BuildWithRegistry(analysis config.AnalysisConfig, profiles map[string]config.LocalAgentProfile, registry *ProviderRegistry) (*CLI, Info, error) {
	return buildWithRunnerAndRegistry(analysis, profiles, OSProcessRunner{}, registry)
}

func buildWithRunner(analysis config.AnalysisConfig, profiles map[string]config.LocalAgentProfile, runner ProcessRunner) (*CLI, Info, error) {
	return buildWithRunnerAndRegistry(analysis, profiles, runner, DefaultProviderRegistry())
}

func buildWithRunnerAndRegistry(analysis config.AnalysisConfig, profiles map[string]config.LocalAgentProfile, runner ProcessRunner, registry *ProviderRegistry) (*CLI, Info, error) {
	if runner == nil {
		return nil, Info{}, fmt.Errorf("local agent process runner is nil")
	}
	if registry == nil {
		registry = DefaultProviderRegistry()
	}
	profileName, profile, err := resolveProfile(analysis, profiles, registry, requiresCommandLookup(runner))
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
	adapter := registry.Lookup(provider)
	if adapter == nil {
		adapter = genericAdapter{name: provider}
	}
	configuredCommand := strings.TrimSpace(profile.Command)
	command := configuredCommand
	if command == "" {
		command = adapter.DefaultCommand()
	}
	if requiresCommandLookup(runner) {
		resolvedCommand, err := exec.LookPath(command)
		if err != nil {
			return nil, Info{}, fmt.Errorf("local agent %q is not available as %q; install it or set analysis.profiles.%s.local_agent.command: %w", provider, command, profileName, err)
		}
		command = resolvedCommand
	}
	commandPrefix := []string(nil)
	if requiresCommandLookup(runner) && configuredCommand == "" {
		command, commandPrefix = adapter.ResolveDefaultCommand(command)
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
		args = adapter.DefaultArgs(persistent)
	}
	return &CLI{
		profileName:     profileName,
		provider:        provider,
		adapter:         adapter,
		imageTransport:  profile.ImageTransport,
		command:         command,
		commandPrefix:   append([]string(nil), commandPrefix...),
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
		if attempt == attempts-1 || !c.adapter.Retryable(err) {
			return analyzer.Result{}, c.adapter.DiagnoseError(err)
		}
		if err := waitRetry(ctx, attempt); err != nil {
			return analyzer.Result{}, c.adapter.DiagnoseError(err)
		}
	}
	return analyzer.Result{}, c.adapter.DiagnoseError(lastErr)
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
	var image *artifact.Image
	imagePath := ""
	processDir := workDir
	if len(request.Image) > 0 {
		image, err = artifact.StageInDir(request.Image, request.MIMEType, workDir)
		if err != nil {
			return analyzer.Result{}, err
		}
		defer func() {
			if cleanupErr := image.Cleanup(); cleanupErr != nil {
				slog.Warn("failed to clean local agent screenshot artifact", "path", image.Path, "error", cleanupErr)
			}
		}()
		imagePath = image.Path
		if workDir == "" {
			workDir = image.Dir
			processDir = image.Dir
		}
		if configuredWorkDir {
			processDir = workDir
		}
	}
	prompt = c.adapter.PreparePrompt(prompt, imagePath, workDir, c.imageTransport)
	c.mu.Lock()
	sessionID := c.sessionID
	c.mu.Unlock()
	args := c.args
	if c.builtInArgs && imagePath == "" {
		args = c.adapter.DefaultArgsWithoutImage(c.persistent)
	}
	args = expandArgs(args, prompt, imagePath, workDir, sessionID)
	if c.persistent && c.builtInArgs && sessionID != "" {
		args = c.adapter.ResumeArgs(prompt, imagePath, sessionID)
	}
	if imagePath != "" {
		args = c.adapter.PrepareArgs(args, imagePath, prompt)
	} else if !c.builtInArgs {
		args = removeImageAttachmentArgs(args)
	}
	processArgs := append([]string(nil), c.commandPrefix...)
	processArgs = append(processArgs, args...)
	imageFileReady := false
	if imagePath != "" {
		if info, statErr := os.Stat(imagePath); statErr == nil {
			imageFileReady = info.Size() > 0
		}
	}
	envAdditions := c.adapter.Environment(c.env, c.maxOutputTokens)
	for key, value := range map[string]string{
		"SCREENLENS_SOURCE":   request.Source,
		"SCREENLENS_PROVIDER": c.provider,
	} {
		if envAdditions == nil {
			envAdditions = make(map[string]string)
		}
		envAdditions[key] = value
	}
	if imagePath != "" {
		if envAdditions == nil {
			envAdditions = make(map[string]string)
		}
		envAdditions["SCREENLENS_IMAGE_PATH"] = imagePath
	}
	env := mergedEnv(c.env, envAdditions)
	callCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	slog.Info("local agent request prepared",
		"provider", c.provider,
		"profile", c.profileName,
		"command", c.command,
		"args", diagnosticArgs(processArgs, prompt, imagePath, workDir),
		"image_attached", imagePath != "",
		"image_bytes", len(request.Image),
		"image_file_ready", imageFileReady,
		"prompt_bytes", len(prompt),
		"workdir_configured", configuredWorkDir,
	)
	started := time.Now()
	processResult, err := c.runner.Run(callCtx, ProcessSpec{
		Command:        c.command,
		Args:           processArgs,
		Dir:            processDir,
		Env:            env,
		MaxOutputBytes: c.maxOutput,
	})
	if err != nil {
		slog.Error("local agent request failed",
			"provider", c.provider,
			"profile", c.profileName,
			"duration", time.Since(started),
			"error", err,
		)
		diagnostic := strings.TrimSpace(string(processResult.Stderr))
		if diagnostic == "" {
			diagnostic = strings.TrimSpace(string(processResult.Stdout))
		}
		if diagnostic != "" {
			return analyzer.Result{}, fmt.Errorf("%w: %s", err, truncate(diagnostic))
		}
		return analyzer.Result{}, err
	}
	result, err := c.adapter.ParseOutput(processResult.Stdout)
	if err != nil {
		if len(processResult.Stderr) > 0 {
			return analyzer.Result{}, fmt.Errorf("%w: %s", err, truncate(string(processResult.Stderr)))
		}
		return analyzer.Result{}, err
	}
	result.ExitCode = processResult.ExitCode
	result.Duration = time.Since(started)
	slog.Info("local agent request completed",
		"provider", c.provider,
		"profile", c.profileName,
		"duration", result.Duration,
		"exit_code", result.ExitCode,
		"output_bytes", len(processResult.Stdout),
	)
	if c.persistent && result.SessionID != "" {
		c.mu.Lock()
		c.sessionID = result.SessionID
		c.mu.Unlock()
	}
	return result, nil
}

func diagnosticArgs(args []string, prompt, imagePath, workDir string) []string {
	result := make([]string, 0, len(args))
	redactNext := false
	for _, arg := range args {
		if redactNext {
			result = append(result, "<redacted>")
			redactNext = false
			continue
		}
		if sensitiveArgument(arg) {
			if strings.Contains(arg, "=") {
				result = append(result, strings.SplitN(arg, "=", 2)[0]+"=<redacted>")
			} else {
				result = append(result, arg)
				redactNext = true
			}
			continue
		}
		redacted := arg
		if prompt != "" {
			redacted = strings.ReplaceAll(redacted, prompt, "<prompt>")
		}
		if imagePath != "" {
			redacted = strings.ReplaceAll(redacted, imagePath, "<image>")
		}
		if workDir != "" {
			redacted = strings.ReplaceAll(redacted, workDir, "<workdir>")
		}
		result = append(result, redacted)
	}
	return result
}

func sensitiveArgument(arg string) bool {
	lower := strings.ToLower(strings.TrimSpace(arg))
	for _, marker := range []string{"api-key", "api_key", "apikey", "token", "secret", "password", "authorization", "bearer"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
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

func resolveProfile(analysis config.AnalysisConfig, profiles map[string]config.LocalAgentProfile, registry *ProviderRegistry, checkCommands bool) (string, config.LocalAgentProfile, error) {
	if registry == nil {
		registry = DefaultProviderRegistry()
	}
	all := builtInProfiles(registry)
	for name, profile := range profiles {
		all[strings.ToLower(strings.TrimSpace(name))] = profile
	}
	name := strings.ToLower(strings.TrimSpace(analysis.Profile))
	if name == "" {
		name = config.LocalAgentProviderAuto
	}
	if name == config.LocalAgentProviderAuto {
		candidates := analysis.AutoProfiles
		if len(candidates) == 0 {
			candidates = config.DefaultAutoProfiles()
		}
		for _, candidate := range candidates {
			candidate = strings.ToLower(strings.TrimSpace(candidate))
			if candidate == "" {
				continue
			}
			profile := all[candidate]
			command := profile.Command
			if command == "" {
				provider := normalizeProvider(profile.Provider)
				if provider == "" {
					provider = candidate
				}
				if adapter := registry.Lookup(provider); adapter != nil {
					command = adapter.DefaultCommand()
				} else {
					command = provider
				}
			}
			if !checkCommands {
				return candidate, profile, nil
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

func builtInProfiles(registry *ProviderRegistry) map[string]config.LocalAgentProfile {
	profiles := make(map[string]config.LocalAgentProfile)
	if registry == nil {
		registry = DefaultProviderRegistry()
	}
	for _, name := range registry.Names() {
		profiles[name] = config.LocalAgentProfile{Provider: name, Transport: config.LocalAgentTransportCLI}
	}
	return profiles
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
