package agent

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/th1nk-er/ScreenLens/internal/analyzer"
)

const parserLineLimit = 4 << 20

func parseCodex(data []byte) (analyzer.Result, error) {
	result := analyzer.Result{Provider: "codex"}
	var messages []string
	var parseErrors []string
	forEachJSONLine(data, func(event map[string]any) {
		typ := stringField(event, "type")
		if result.SessionID == "" {
			result.SessionID = firstString(event, "thread_id", "threadId", "session_id", "sessionId")
		}
		if typ == "thread.started" && result.SessionID == "" {
			result.SessionID = firstString(event, "id")
		}
		if strings.Contains(strings.ToLower(typ), "error") {
			if message := eventMessage(event); message != "" {
				parseErrors = append(parseErrors, message)
			}
		}
		if typ == "item.completed" || typ == "item.updated" || typ == "agent_message" {
			item := event
			if nested, ok := event["item"].(map[string]any); ok {
				item = nested
			}
			itemType := strings.ToLower(stringField(item, "type"))
			if itemType == "agent_message" || itemType == "assistant_message" || typ == "agent_message" {
				if text := messageText(item); text != "" {
					messages = append(messages, text)
				}
			}
		}
	})
	if len(messages) == 0 {
		if text := strings.TrimSpace(string(data)); text != "" && !looksLikeJSONL(data) {
			return analyzer.Result{Provider: "codex", Text: text, SessionID: result.SessionID}, nil
		}
		if len(parseErrors) > 0 {
			return analyzer.Result{}, fmt.Errorf("codex returned an error: %s", strings.Join(parseErrors, "; "))
		}
		return analyzer.Result{}, errors.New("codex returned no assistant message")
	}
	result.Text = strings.TrimSpace(strings.Join(uniqueStrings(messages), "\n\n"))
	return result, nil
}

func parseClaude(data []byte) (analyzer.Result, error) {
	var payload struct {
		Type      string `json:"type"`
		Result    string `json:"result"`
		SessionID string `json:"session_id"`
		IsError   bool   `json:"is_error"`
		Error     string `json:"error"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(data), &payload); err == nil {
		if payload.IsError || strings.EqualFold(payload.Type, "error") {
			message := strings.TrimSpace(payload.Error)
			if message == "" {
				message = strings.TrimSpace(payload.Result)
			}
			if message == "" {
				message = "claude returned an error"
			}
			return analyzer.Result{}, errors.New(message)
		}
		if text := strings.TrimSpace(payload.Result); text != "" {
			return analyzer.Result{Provider: "claude", Text: text, SessionID: payload.SessionID}, nil
		}
		return analyzer.Result{}, errors.New("claude returned no result")
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return analyzer.Result{}, errors.New("claude returned an empty result")
	}
	return analyzer.Result{Provider: "claude", Text: text}, nil
}

func parseOpenCode(data []byte) (analyzer.Result, error) {
	result := analyzer.Result{Provider: "opencode"}
	var messages []string
	var eventErrors []string
	forEachJSONLine(data, func(event map[string]any) {
		typ := strings.ToLower(stringField(event, "type"))
		if result.SessionID == "" {
			result.SessionID = firstString(event, "session_id", "sessionID", "sessionId")
			if result.SessionID == "" && strings.Contains(typ, "session") {
				result.SessionID = firstString(event, "id")
			}
		}
		if strings.Contains(typ, "error") {
			if message := eventMessage(event); message != "" {
				eventErrors = append(eventErrors, message)
			}
		}
		if typ == "text" || typ == "assistant" || typ == "assistant_message" || typ == "message" || typ == "part" {
			if text := messageText(event); text != "" {
				messages = append(messages, text)
			}
		}
		if part, ok := event["part"].(map[string]any); ok && (typ == "message" || typ == "text" || typ == "assistant") {
			if text := messageText(part); text != "" {
				messages = append(messages, text)
			}
		}
	})
	if len(messages) == 0 {
		if text := strings.TrimSpace(string(data)); text != "" && !looksLikeJSONL(data) {
			return analyzer.Result{Provider: "opencode", Text: text, SessionID: result.SessionID}, nil
		}
		if len(eventErrors) > 0 {
			return analyzer.Result{}, fmt.Errorf("opencode returned an error: %s", strings.Join(eventErrors, "; "))
		}
		return analyzer.Result{}, errors.New("opencode returned no assistant message")
	}
	result.Text = strings.TrimSpace(strings.Join(uniqueStrings(messages), "\n\n"))
	return result, nil
}

func forEachJSONLine(data []byte, visit func(map[string]any)) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 64*1024), parserLineLimit)
	for scanner.Scan() {
		var event map[string]any
		if json.Unmarshal(scanner.Bytes(), &event) == nil && event != nil {
			visit(event)
		}
	}
}

func looksLikeJSONL(data []byte) bool {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return false
	}
	var value any
	if json.Unmarshal(trimmed, &value) == nil {
		switch value.(type) {
		case map[string]any, []any:
			return true
		}
	}
	scanner := bufio.NewScanner(bytes.NewReader(trimmed))
	scanner.Buffer(make([]byte, 64*1024), parserLineLimit)
	for scanner.Scan() {
		var event map[string]any
		if json.Unmarshal(scanner.Bytes(), &event) == nil && event != nil {
			return true
		}
	}
	return false
}

func stringField(values map[string]any, key string) string {
	value, _ := values[key].(string)
	return strings.TrimSpace(value)
}

func firstString(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := stringField(values, key); value != "" {
			return value
		}
	}
	return ""
}

func messageText(values map[string]any) string {
	for _, key := range []string{"text", "content", "delta", "message"} {
		if value := stringField(values, key); value != "" {
			return value
		}
	}
	if content, ok := values["content"].([]any); ok {
		var parts []string
		for _, value := range content {
			if object, ok := value.(map[string]any); ok {
				if text := messageText(object); text != "" {
					parts = append(parts, text)
				}
			}
		}
		return strings.TrimSpace(strings.Join(parts, "\n"))
	}
	return ""
}

func eventMessage(event map[string]any) string {
	if message := messageText(event); message != "" {
		return message
	}
	if value, ok := event["error"].(map[string]any); ok {
		return messageText(value)
	}
	if value, ok := event["error"].(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
