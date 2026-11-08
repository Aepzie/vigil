package screens

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/Aepzie/vigil/internal/theme"
)

// testForm has four fields: a, b (hidden), c (disabled) and d.
func testForm() *form {
	return &form{
		title: "Test",
		fields: []formField{
			{label: "A", input: newInput("")},
			{label: "Bbbbbbbbbbbbbbb", input: newInput(""), hidden: true},
			{label: "C", input: newInput(""), disabled: "not now"},
			{label: "D", input: newInput("")},
		},
	}
}

func TestFormSkipsHiddenAndDisabledFields(t *testing.T) {
	tests := []struct {
		name       string
		keys       []string
		wantFocus  int
		wantSubmit bool
	}{
		{"tab", []string{"tab"}, 3, false},
		{"tab wraps", []string{"tab", "tab"}, 0, false},
		{"shift+tab wraps", []string{"shift+tab"}, 3, false},
		{"enter advances", []string{"enter"}, 3, false},
		{"enter on last focusable submits", []string{"enter", "enter"}, 3, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := testForm()
			submitted := false
			for _, k := range tt.keys {
				submitted = f.Update(press(k)) == formSubmit
			}
			if f.focus != tt.wantFocus || submitted != tt.wantSubmit {
				t.Fatalf("focus %d submit %v, want %d %v", f.focus, submitted, tt.wantFocus, tt.wantSubmit)
			}
		})
	}
}

func TestFormViewHiddenAndDisabled(t *testing.T) {
	th := theme.Default()
	f := testForm()
	f.keys = "enter go · esc stop"
	for _, size := range [][2]int{{60, 20}, {60, 7}, {30, 5}} {
		pw, ph := f.Size(size[0], size[1])
		out := f.View(th, pw, ph)
		checkSize(t, out, pw, ph)
		plain := ansi.Strip(out)
		if strings.Contains(plain, "Bbbb") {
			t.Fatalf("hidden field rendered:\n%s", plain)
		}
		if size[1] >= 20 {
			if !strings.Contains(plain, "not now") || !strings.Contains(plain, "enter go · esc stop") {
				t.Fatalf("disabled note or key help missing:\n%s", plain)
			}
		}
	}
	if w, h := f.Size(80, 40); w != maxFormWidth || h != 2*3+4 {
		t.Fatalf("Size = %d×%d, want hidden fields excluded", w, h)
	}
}
