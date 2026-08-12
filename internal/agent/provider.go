package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/th1nk-er/ScreenLens/internal/analyzer"
)

var errEmptyAgentResult = errors.New("local agent returned an empty result")

func formatInt(value int) string { return strconv.Itoa(value) }

// ProviderAdapter contains the provider-specific parts of a local CLI
// integration. CLI owns lifecycle concerns; adapters own command syntax,
// native image transport, environment defaults, and output decoding.
//
// Keeping this boundary explicit makes adding a provider a localized change
// and prevents the execution loop from accumulating provider conditionals.
type ProviderAdapter interface {
	Name() string
	DefaultCommand() string
	ResolveDefaultCommand(command string) (string, []string)
	DefaultArgs(persistent bool) []string
	DefaultArgsWithoutImage(persistent bool) []string
	ResumeArgs(prompt, imagePath, sessionID string) []string
	PreparePrompt(prompt, imagePath, workDir, imageTransport string) string
	PrepareArgs(args []string, imagePath, prompt string) []string
	Environment(overrides map[string]string, maxOutputTokens int) map[string]string
	ParseOutput(data []byte) (analyzer.Result, error)
	Retryable(err error) bool
	DiagnoseError(err error) error
}

// ProviderRegistry is intentionally small and immutable after construction in
// normal application use. Tests and future integrations can provide a custom
// registry without changing CLI orchestration.
type ProviderRegistry struct {
	providers map[string]ProviderAdapter
}

func NewProviderRegistry(adapters ...ProviderAdapter) *ProviderRegistry {
	providers := make(map[string]ProviderAdapter, len(adapters))
	for _, adapter := range adapters {
		if adapter == nil {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(adapter.Name()))
		if name != "" {
			providers[name] = adapter
		}
	}
	return &ProviderRegistry{providers: providers}
}

func (r *ProviderRegistry) Lookup(name string) ProviderAdapter {
	if r == nil {
		return nil
	}
	return r.providers[strings.ToLower(strings.TrimSpace(name))]
}

func (r *ProviderRegistry) Names() []string {
	if r == nil {
		return nil
	}
	names := make([]string, 0, len(r.providers))
	for name := range r.providers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func DefaultProviderRegistry() *ProviderRegistry {
	return NewProviderRegistry(codexAdapter{}, claudeAdapter{}, openCodeAdapter{})
}

type genericAdapter struct{ name string }

func (a genericAdapter) Name() string { return a.name }

func (a genericAdapter) DefaultCommand() string { return a.name }

func (a genericAdapter) ResolveDefaultCommand(command string) (string, []string) {
	return command, nil
}

func (a genericAdapter) DefaultArgs(bool) []string { return []string{placeholderPrompt} }

func (a genericAdapter) DefaultArgsWithoutImage(bool) []string {
	return []string{placeholderPrompt}
}

func (a genericAdapter) ResumeArgs(prompt, _, _ string) []string { return []string{prompt} }

func (a genericAdapter) PreparePrompt(prompt, imagePath, workDir, imageTransport string) string {
	return promptWithPathFallback(prompt, imagePath, workDir, imageTransport)
}

func (a genericAdapter) PrepareArgs(args []string, _, _ string) []string { return args }

func (a genericAdapter) Environment(map[string]string, int) map[string]string { return nil }

func (a genericAdapter) ParseOutput(data []byte) (analyzer.Result, error) {
	text := strings.TrimSpace(string(data))
	if text == "" {
		return analyzer.Result{}, errEmptyAgentResult
	}
	return analyzer.Result{Text: text}, nil
}

func (a genericAdapter) Retryable(err error) bool { return retryableProviderError(err) }

func (a genericAdapter) DiagnoseError(err error) error {
	return diagnoseProviderError(a.name, err)
}

type codexAdapter struct{}

func (codexAdapter) Name() string { return "codex" }

func (codexAdapter) DefaultCommand() string { return "codex" }

func (codexAdapter) ResolveDefaultCommand(command string) (string, []string) {
	if runtime.GOOS != "windows" || !strings.EqualFold(filepath.Ext(command), ".cmd") {
		return command, nil
	}
	script := strings.TrimSuffix(command, filepath.Ext(command)) + ".ps1"
	if _, err := os.Stat(script); err != nil {
		return command, nil
	}
	powershell, err := exec.LookPath("powershell.exe")
	if err != nil {
		return command, nil
	}
	return powershell, []string{"-NoProfile", "-ExecutionPolicy", "Bypass", "-File", script}
}

func (codexAdapter) DefaultArgs(persistent bool) []string {
	args := []string{"exec", "--json", "--sandbox", "read-only", "--skip-git-repo-check", "--ignore-user-config"}
	if !persistent {
		args = append(args, "--ephemeral")
	}
	return append(args, placeholderPrompt, "--image", placeholderImagePath)
}

func (codexAdapter) DefaultArgsWithoutImage(persistent bool) []string {
	args := []string{"exec", "--json", "--sandbox", "read-only", "--skip-git-repo-check", "--ignore-user-config"}
	if !persistent {
		args = append(args, "--ephemeral")
	}
	return append(args, placeholderPrompt)
}

func (codexAdapter) ResumeArgs(prompt, imagePath, sessionID string) []string {
	args := []string{"exec", "--skip-git-repo-check", "--ignore-user-config", "resume", sessionID, "--json", "--sandbox", "read-only", prompt}
	if imagePath != "" {
		args = append(args, "--image", imagePath)
	}
	return args
}

func (codexAdapter) PreparePrompt(prompt, _, _, _ string) string { return prompt }

func (codexAdapter) PrepareArgs(args []string, imagePath, prompt string) []string {
	if imagePath == "" {
		return args
	}
	return ensureCodexAttachment(args, imagePath, prompt)
}

func (codexAdapter) Environment(map[string]string, int) map[string]string { return nil }

func (codexAdapter) ParseOutput(data []byte) (analyzer.Result, error) {
	return parseCodex(data)
}

func (codexAdapter) Retryable(err error) bool { return retryableProviderError(err) }

func (codexAdapter) DiagnoseError(err error) error { return diagnoseProviderError("codex", err) }

type claudeAdapter struct{}

func (claudeAdapter) Name() string { return "claude" }

func (claudeAdapter) DefaultCommand() string { return "claude" }

func (claudeAdapter) ResolveDefaultCommand(command string) (string, []string) {
	return command, nil
}

func (claudeAdapter) DefaultArgs(persistent bool) []string {
	if persistent {
		return []string{"-p", placeholderPrompt, "--output-format", "json", "--permission-mode", "dontAsk", "--tools", "Read", "--allowed-tools", "Read", "--strict-mcp-config"}
	}
	return []string{"-p", placeholderPrompt, "--output-format", "json", "--no-session-persistence", "--permission-mode", "dontAsk", "--tools", "Read", "--allowed-tools", "Read", "--strict-mcp-config"}
}

func (claudeAdapter) DefaultArgsWithoutImage(persistent bool) []string {
	return claudeAdapter{}.DefaultArgs(persistent)
}

func (claudeAdapter) ResumeArgs(prompt, _, sessionID string) []string {
	return []string{"--resume", sessionID, "-p", prompt, "--output-format", "json", "--permission-mode", "dontAsk", "--tools", "Read", "--allowed-tools", "Read", "--strict-mcp-config"}
}

func (claudeAdapter) PreparePrompt(prompt, imagePath, _, _ string) string {
	if imagePath == "" {
		return prompt
	}
	return attachClaudeImage(prompt, imagePath)
}

func (claudeAdapter) PrepareArgs(args []string, _, _ string) []string { return args }

func (claudeAdapter) Environment(overrides map[string]string, maxOutputTokens int) map[string]string {
	if _, configured := overrides["CLAUDE_CODE_MAX_OUTPUT_TOKENS"]; configured {
		return nil
	}
	return map[string]string{"CLAUDE_CODE_MAX_OUTPUT_TOKENS": formatInt(maxOutputTokens)}
}

func (claudeAdapter) ParseOutput(data []byte) (analyzer.Result, error) {
	return parseClaude(data)
}

func (claudeAdapter) Retryable(err error) bool { return retryableProviderError(err) }

func (claudeAdapter) DiagnoseError(err error) error { return diagnoseProviderError("claude", err) }

type openCodeAdapter struct{}

func (openCodeAdapter) Name() string { return "opencode" }

func (openCodeAdapter) DefaultCommand() string { return "opencode" }

func (openCodeAdapter) ResolveDefaultCommand(command string) (string, []string) {
	return command, nil
}

func (openCodeAdapter) DefaultArgs(bool) []string {
	return []string{"--pure", "run", "--format", "json", placeholderPrompt, "--file", placeholderImagePath}
}

func (openCodeAdapter) DefaultArgsWithoutImage(bool) []string {
	return []string{"--pure", "run", "--format", "json", placeholderPrompt}
}

func (openCodeAdapter) ResumeArgs(prompt, imagePath, sessionID string) []string {
	args := []string{"--pure", "run", "--session", sessionID, "--format", "json", prompt}
	if imagePath != "" {
		args = append(args, "--file", imagePath)
	}
	return args
}

func (openCodeAdapter) PreparePrompt(prompt, _, _, _ string) string { return prompt }

func (openCodeAdapter) PrepareArgs(args []string, imagePath, prompt string) []string {
	if imagePath == "" {
		return args
	}
	return ensureOpenCodeAttachment(args, imagePath, prompt)
}

func (openCodeAdapter) Environment(overrides map[string]string, _ int) map[string]string {
	return openCodeEnvironment(overrides)
}

func openCodeEnvironment(overrides map[string]string) map[string]string {
	defaults := map[string]string{
		// A screenshot-analysis step should use the native attachment, not search
		// the workspace for words from the prompt. Keep read available for the
		// attached file while disabling discovery-oriented tools by default.
		"OPENCODE_PERMISSION":              `{"*":"deny","read":"allow"}`,
		"OPENCODE_CONFIG_CONTENT":          `{"mcp":{},"permission":{"*":"deny","read":"allow"}}`,
		"OPENCODE_DISABLE_DEFAULT_PLUGINS": "true",
	}
	for key := range defaults {
		if _, configured := overrides[key]; configured {
			delete(defaults, key)
		}
	}
	return defaults
}

func (openCodeAdapter) ParseOutput(data []byte) (analyzer.Result, error) {
	return parseOpenCode(data)
}

func (openCodeAdapter) Retryable(err error) bool { return retryableProviderError(err) }

func (openCodeAdapter) DiagnoseError(err error) error { return diagnoseProviderError("opencode", err) }

func promptWithPathFallback(prompt, imagePath, workDir, imageTransport string) string {
	hasImage := strings.Contains(prompt, placeholderImagePath)
	prompt = strings.ReplaceAll(prompt, placeholderImagePath, imagePath)
	prompt = strings.ReplaceAll(prompt, placeholderWorkDir, workDir)
	if imagePath != "" && !hasImage && !strings.EqualFold(strings.TrimSpace(imageTransport), "native") {
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

func removeImageAttachmentArgs(args []string) []string {
	result := make([]string, 0, len(args))
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == "-i" || arg == "--image" || arg == "-f" || arg == "--file":
			if index+1 < len(args) {
				index++
			}
		case strings.HasPrefix(arg, "-i=") || strings.HasPrefix(arg, "--image=") || strings.HasPrefix(arg, "-f=") || strings.HasPrefix(arg, "--file="):
		default:
			result = append(result, arg)
		}
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
	result := make([]string, 0, len(args)+2)
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if arg == "-i" || arg == "--image" {
			// Codex treats --image as a variadic option. Remove its configured
			// value and append the canonical attachment after the prompt so the
			// prompt cannot be consumed as another image path.
			if index+1 < len(args) && args[index+1] != prompt {
				index++
			}
			continue
		}
		if strings.HasPrefix(arg, "--image=") || strings.HasPrefix(arg, "-i=") {
			continue
		}
		result = append(result, arg)
	}
	return append(result, "--image", imagePath)
}

func retryableProviderError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrOutputLimit) {
		return false
	}
	message := strings.ToLower(err.Error())
	if unavailableModelError(message) {
		return false
	}
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

func diagnoseProviderError(provider string, err error) error {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	message := strings.ToLower(err.Error())
	switch {
	case unavailableModelError(message):
		return fmt.Errorf("local agent %q selected model is unavailable; configure a valid model in the provider CLI or pass one through local_agent.args (for example, --model): %w", provider, err)
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

func unavailableModelError(message string) bool {
	hasNotFoundStatus := strings.Contains(message, "status code 404") || strings.Contains(message, `"api_error_status":404`)
	if hasNotFoundStatus && strings.Contains(message, "model") {
		return true
	}
	return strings.Contains(message, "model") &&
		(strings.Contains(message, "may not exist") ||
			strings.Contains(message, "does not exist") ||
			strings.Contains(message, "not found") ||
			strings.Contains(message, "no access") ||
			strings.Contains(message, "no permission"))
}
