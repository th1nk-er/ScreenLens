package hotkey

import (
	"context"
	"fmt"
	"sort"
	"strings"

	hook "github.com/robotn/gohook"
)

const (
	noMouseButton     uint16 = 0
	mouseButtonX1     uint16 = 4
	mouseButtonX2     uint16 = 5
	minimumHotkeyKeys        = 2
)

type Listener struct {
	bindings []binding
	// These fields remain populated for listeners created by New so the
	// single-binding listener API stays source-compatible with older callers.
	keys        []string
	mouseButton uint16
}

// Binding describes one global shortcut. The callback receives the most
// recently observed desktop mouse position. For mouse-button shortcuts this
// is the position from the triggering mouse event itself.
type Binding struct {
	Combination string
	OnPress     func(x, y int)
}

type binding struct {
	keys        []string
	mouseButton uint16
	onPress     func(x, y int)
}

func New(combination string) (*Listener, error) {
	listener, err := NewBindings([]Binding{{Combination: combination}})
	if err != nil {
		return nil, err
	}
	if button, recognized := parseMouseButton(combination); recognized {
		listener.mouseButton = button
	} else {
		listener.keys, _ = parseCombination(combination)
	}
	return listener, nil
}

// NewBindings validates and creates a listener for several shortcuts sharing
// one OS-level hook. Empty combinations are ignored so callers can optionally
// disable a binding in a larger configuration.
func NewBindings(bindings []Binding) (*Listener, error) {
	listener := &Listener{}
	seen := make(map[string]struct{}, len(bindings))
	for _, candidate := range bindings {
		if strings.TrimSpace(candidate.Combination) == "" {
			continue
		}
		parsed, identity, err := parseBinding(candidate)
		if err != nil {
			return nil, err
		}
		if _, exists := seen[identity]; exists {
			return nil, fmt.Errorf("hotkey %q is configured more than once", candidate.Combination)
		}
		seen[identity] = struct{}{}
		listener.bindings = append(listener.bindings, parsed)
	}
	if len(listener.bindings) == 0 {
		return nil, fmt.Errorf("no hotkeys are configured")
	}
	return listener, nil
}

// Run blocks until ctx is cancelled. The callback only publishes a workflow
// event; screenshot and network work stays outside the global hook callback.
func (l *Listener) Run(ctx context.Context, onCapture func()) error {
	if ctx == nil {
		ctx = context.Background()
	}

	// Keyboard events do not consistently carry the current cursor position
	// on every platform. Tracking mouse events in the same hook gives keyboard
	// shortcuts the position that was current when the shortcut was pressed.
	var mouseX, mouseY int
	hook.Register(hook.MouseMove, nil, func(event hook.Event) {
		mouseX, mouseY = int(event.X), int(event.Y)
	})
	hook.Register(hook.MouseDrag, nil, func(event hook.Event) {
		mouseX, mouseY = int(event.X), int(event.Y)
	})
	for _, candidate := range l.bindings {
		candidate := candidate
		if candidate.mouseButton != noMouseButton {
			hook.Register(hook.MouseDown, nil, func(event hook.Event) {
				if event.Button != candidate.mouseButton {
					return
				}
				mouseX, mouseY = int(event.X), int(event.Y)
				invoke(candidate.onPress, onCapture, mouseX, mouseY)
			})
			continue
		}
		// Match on KeyDown after all keys are pressed. gohook's KeyUp
		// combination bookkeeping can fire when a modifier alone is released,
		// which makes shortcuts trigger on the wrong key. KeyDown fires once
		// when the final key completes the combination and does not repeat on
		// hold.
		hook.Register(hook.KeyDown, candidate.keys, func(hook.Event) {
			invoke(candidate.onPress, onCapture, mouseX, mouseY)
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

func parseBinding(candidate Binding) (binding, string, error) {
	if button, recognized := parseMouseButton(candidate.Combination); recognized {
		return binding{mouseButton: button, onPress: candidate.OnPress}, fmt.Sprintf("mouse:%d", button), nil
	}
	keys, err := parseCombination(candidate.Combination)
	if err != nil {
		return binding{}, "", err
	}
	identityKeys := append([]string(nil), keys...)
	sort.Strings(identityKeys)
	return binding{keys: keys, onPress: candidate.OnPress}, "key:" + strings.Join(identityKeys, "+"), nil
}

func invoke(bindingCallback func(x, y int), fallback func(), x, y int) {
	if bindingCallback != nil {
		bindingCallback(x, y)
		return
	}
	if fallback != nil {
		fallback()
	}
}

func parseMouseButton(value string) (uint16, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "_")
	switch value {
	case "mouse_x1", "mouse4", "mouse_button4", "xbutton1", "side1", "back":
		return mouseButtonX1, true
	case "mouse_x2", "mouse5", "mouse_button5", "xbutton2", "side2", "forward":
		return mouseButtonX2, true
	default:
		return noMouseButton, false
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
	if len(keys) < minimumHotkeyKeys {
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
