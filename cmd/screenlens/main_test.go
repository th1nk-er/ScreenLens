package main

import (
	"testing"

	"github.com/th1nk-er/ScreenLens/internal/config"
)

func TestSameUserIDsIgnoresOrder(t *testing.T) {
	if !sameUserIDs([]int64{3, 1, 2}, []int64{2, 3, 1}) {
		t.Fatal("sameUserIDs() = false, want true")
	}
	if sameUserIDs([]int64{1, 2}, []int64{1, 3}) {
		t.Fatal("sameUserIDs() = true, want false")
	}
}

func TestFormatAgentsIsStableAndSeparatesProfileTypes(t *testing.T) {
	cfg := config.Defaults()
	got := formatAgents(cfg)
	if got != "# Local agent profiles\n\nUse `/screen [profile]` to select one for a single capture.\n\n- **Active:** `auto`\n- `vision` (hosted vision API)\n- `claude`\n- `codex`\n- `opencode`" {
		t.Fatalf("formatAgents() = %q", got)
	}
}
