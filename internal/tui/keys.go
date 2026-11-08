package tui

import (
	"github.com/Aepzie/vigil/internal/tui/ui"
)

// Global key bindings, matched against tea.KeyPressMsg.String().
const (
	keyForceQuit  = "ctrl+c"
	keyQuit       = "q"
	keyNextTab    = "tab"
	keyPrevTab    = "shift+tab"
	keyPalette    = ":"
	keyPaletteAlt = "ctrl+k"
	keyCompat     = "f2"
	keyHelp       = "?"
	keyEsc        = "esc"
	keyEnter      = "enter"
	keyUp         = "up"
	keyDown       = "down"
	keyPrev       = "ctrl+p"
	keyNext       = "ctrl+n"
	keyBackspace  = "backspace"
	keyClear      = "ctrl+u"
)

// globalHints are appended to the active screen's hints in the footer.
var globalHints = []ui.Hint{
	{Key: "tab", Desc: "switch"},
	{Key: ":", Desc: "palette"},
	{Key: "f2", Desc: "compat"},
	{Key: "?", Desc: "help"},
	{Key: "q", Desc: "quit"},
}

// globalHelp lists the global bindings shown in the help overlay.
var globalHelp = []ui.Hint{
	{Key: "tab / shift+tab", Desc: "next / previous tab"},
	{Key: "1-9", Desc: "jump to tab"},
	{Key: ": / ctrl+k", Desc: "command palette"},
	{Key: "f2", Desc: "cycle compatibility mode"},
	{Key: "?", Desc: "toggle this help"},
	{Key: "q / ctrl+c", Desc: "quit"},
}

// tabIndexForKey maps the digit keys "1".."9" to a tab index below n.
func tabIndexForKey(key string, n int) (int, bool) {
	if len(key) != 1 || key[0] < '1' || key[0] > '9' {
		return 0, false
	}
	idx := int(key[0] - '1')
	return idx, idx < n
}
