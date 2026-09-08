package darwin

import "testing"

// HotkeyDisplay is pure string formatting — the one piece of hotkey.go that has no cgo in it and
// can be table-tested (docs/ARCHITECTURE.md keeps the rest of internal/platform out of unit tests).
// Collected here under V6.6.

func TestHotkeyDisplay(t *testing.T) {
	const (
		ctrl   = 1 << 18
		option = 1 << 19
		shift  = 1 << 17
		cmd    = 1 << 20
	)
	tests := []struct {
		keyCode, mods int
		want          string
	}{
		{48, option, "⌥⇥"},          // the default chord
		{48, option | shift, "⌥⇧⇥"}, // ⇧ = backwards
		{48, ctrl | option, "⌃⌥⇥"},
		{48, ctrl | option | shift | cmd, "⌃⌥⇧⌘⇥"}, // fixed symbol order
		{49, option, "⌥Space"},
		{36, option, "⌥↩"},
		{53, 0, "⎋"},
		{51, cmd, "⌘⌫"},
		{123, option, "⌥←"},
		{124, option, "⌥→"},
		{125, option, "⌥↓"},
		{126, option, "⌥↑"},
		{40, ctrl | option, "⌃⌥ 40"}, // no well-known name → the number, space-separated
		{0, option, "⌥ 0"},
	}
	for _, tt := range tests {
		if got := HotkeyDisplay(tt.keyCode, tt.mods); got != tt.want {
			t.Errorf("HotkeyDisplay(%d, %#x) = %q, want %q", tt.keyCode, tt.mods, got, tt.want)
		}
	}
}
