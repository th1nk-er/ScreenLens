package hotkey

import "testing"

func TestParseCombination(t *testing.T) {
	keys, err := parseCombination("CTRL+SHIFT+S")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"ctrl", "shift", "s"}
	if len(keys) != len(want) {
		t.Fatalf("keys = %v, want %v", keys, want)
	}
	for i := range want {
		if keys[i] != want[i] {
			t.Fatalf("keys = %v, want %v", keys, want)
		}
	}
}

func TestParseCombinationRejectsMalformedInput(t *testing.T) {
	for _, value := range []string{"", "CTRL", "CTRL++S", "CTRL+CTRL"} {
		if _, err := parseCombination(value); err == nil {
			t.Errorf("parseCombination(%q) error = nil", value)
		}
	}
}

func TestParseMouseButton(t *testing.T) {
	tests := map[string]uint16{
		"MOUSE_X1":      4,
		"mouse4":        4,
		"XBUTTON1":      4,
		"MOUSE-X2":      5,
		"mouse_button5": 5,
		"forward":       5,
	}
	for value, want := range tests {
		got, ok := parseMouseButton(value)
		if !ok || got != want {
			t.Errorf("parseMouseButton(%q) = (%d, %t), want (%d, true)", value, got, ok, want)
		}
	}
}

func TestNewMouseListener(t *testing.T) {
	listener, err := New("MOUSE_X1")
	if err != nil {
		t.Fatal(err)
	}
	if listener.mouseButton != 4 {
		t.Fatalf("mouseButton = %d, want 4", listener.mouseButton)
	}
}
