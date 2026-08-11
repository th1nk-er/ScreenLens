package agent

import (
	"testing"

	"github.com/th1nk-er/ScreenLens/internal/analyzer"
)

func TestParseCodexJSONL(t *testing.T) {
	result, err := parseCodex([]byte(`{"type":"thread.started","thread_id":"thread-1"}
{"type":"item.completed","item":{"type":"agent_message","text":"answer"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.Provider != "codex" || result.SessionID != "thread-1" || result.Text != "answer" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestParseClaudeJSON(t *testing.T) {
	result, err := parseClaude([]byte(`{"type":"result","subtype":"success","result":"answer","session_id":"session-1"}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.Provider != "claude" || result.SessionID != "session-1" || result.Text != "answer" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestParseOpenCodeJSONEvents(t *testing.T) {
	result, err := parseOpenCode([]byte(`{"type":"session.created","id":"session-1"}
{"type":"text","part":{"text":"answer"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.Provider != "opencode" || result.SessionID != "session-1" || result.Text != "answer" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestParseRejectsEmptyMachineOutput(t *testing.T) {
	if _, err := parseCodex([]byte(`{"type":"turn.completed"}`)); err == nil {
		t.Fatal("expected empty Codex output to fail")
	}
}

func TestParsePlainMarkdownThatStartsWithBracket(t *testing.T) {
	for _, parse := range []struct {
		name string
		fn   func([]byte) (analyzer.Result, error)
	}{
		{name: "codex", fn: parseCodex},
		{name: "opencode", fn: parseOpenCode},
	} {
		t.Run(parse.name, func(t *testing.T) {
			result, err := parse.fn([]byte("[Answer]\nThe screenshot is readable."))
			if err != nil {
				t.Fatal(err)
			}
			if result.Text != "[Answer]\nThe screenshot is readable." {
				t.Fatalf("text = %q", result.Text)
			}
		})
	}
}

func TestParseProviderErrorMessageField(t *testing.T) {
	if _, err := parseOpenCode([]byte(`{"type":"error","error":"image attachment failed"}`)); err == nil || err.Error() != "opencode returned an error: image attachment failed" {
		t.Fatalf("error = %v", err)
	}
}
