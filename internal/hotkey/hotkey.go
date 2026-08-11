package hotkey

import (
	"context"
	"fmt"
	"strings"

	hook "github.com/robotn/gohook"
)

type Listener struct {
	keys        []string
	mouseButton uint16
}

func New(combination string) (*Listener, error) {
	if button, recognized := parseMouseButton(combination); recognized {
		return &Listener{mouseButton: button}, nil
	}
	keys, err := parseCombination(combination)
	if err != nil {
		return nil, err
	}
	return &Listener{keys: keys}, nil
}

// Run blocks until ctx is cancelled. The callback only publishes a workflow
// event; screenshot and network work stays outside the global hook callback.
func (l *Listener) Run(ctx context.Context, onCapture func()) error {
	if l.mouseButton != 0 {
		hook.Register(hook.MouseDown, nil, func(event hook.Event) {
			if event.Button == l.mouseButton && onCapture != nil {
				onCapture()
			}
		})
	} else {
		// Match on KeyDown after all keys are pressed. gohook's KeyUp combination
		// bookkeeping can fire when a modifier alone is released, which makes a
		// shortcut such as CTRL+SHIFT+S trigger on CTRL. KeyDown only fires once
		// when the final key completes the combination and does not repeat on hold.
		hook.Register(hook.KeyDown, l.keys, func(hook.Event) {
			if onCapture != nil {
				onCapture()
			}
		})
	}
	events := hook.Start()
	processed := hook.Process(events)

	select {
	case <-ctx.Done():
		hook.End()
		<-processed
		return nil
	case <-processed:
		return nil
	}
}

func parseMouseButton(value string) (uint16, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "_")
	switch value {
	case "mouse_x1", "mouse4", "mouse_button4", "xbutton1", "side1", "back":
		return 4, true
	case "mouse_x2", "mouse5", "mouse_button5", "xbutton2", "side2", "forward":
		return 5, true
	default:
		return 0, false
	}
}

func parseCombination(value string) ([]string, error) {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(value)), "+")
	if len(parts) == 0 {
		return nil, fmt.Errorf("hotkey is empty")
	}
	keys := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		key := strings.TrimSpace(part)
		if key == "" {
			return nil, fmt.Errorf("hotkey %q contains an empty key", value)
		}
		key = normalizeKey(key)
		if hook.Keycode[key] == 0 {
			return nil, fmt.Errorf("hotkey %q contains unsupported key %q", value, key)
		}
		if _, exists := seen[key]; exists {
			return nil, fmt.Errorf("hotkey %q contains duplicate key %q", value, key)
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	if len(keys) < 2 {
		return nil, fmt.Errorf("hotkey %q must contain at least two keys", value)
	}
	return keys, nil
}

func normalizeKey(key string) string {
	switch key {
	case "control":
		return "ctrl"
	case "option":
		return "alt"
	case "windows", "win", "super", "command", "cmd":
		return "l-super"
	default:
		return key
	}
}
