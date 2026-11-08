package main

import (
	"bytes"
	"errors"
	"flag"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/CraguS/vigil/internal/auth"
	"github.com/CraguS/vigil/internal/compat"
)

func testIO(stdin string) (stdio, *bytes.Buffer, *bytes.Buffer) {
	var out, errOut bytes.Buffer
	return stdio{in: strings.NewReader(stdin), out: &out, err: &errOut}, &out, &errOut
}

func TestDispatch(t *testing.T) {
	tests := []struct {
		args    []string
		wantOut string
		wantErr bool
	}{
		{[]string{"help"}, "Usage:", false},
		{[]string{"--help"}, "Usage:", false},
		{[]string{"version"}, "vigil ", false},
		{[]string{"--version"}, "vigil ", false},
		{[]string{"frobnicate"}, "", true},
		{[]string{"render", "--size", "nope"}, "", true},
		{[]string{"render", "--tab", "nope"}, "", true},
		{[]string{"render", "extra"}, "", true},
		{[]string{"render", "-h"}, "", false},
		{[]string{"user"}, "Usage:", true},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			std, out, _ := testIO("")
			err := dispatch(tt.args, std)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if !strings.Contains(out.String(), tt.wantOut) {
				t.Fatalf("stdout = %q, want it to contain %q", out.String(), tt.wantOut)
			}
		})
	}
}

func TestRender(t *testing.T) {
	std, out, _ := testIO("")
	if err := dispatch([]string{"render", "--size", "80x24", "--tab", "alerts", "--compat", "ascii", "--seed", "7"}, std); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(out.String(), "\x1b[0m\n") {
		t.Fatalf("frame does not end with a reset and newline: %q", out.String())
	}
}

func TestParseFlagsInterleaved(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	data := fs.String("data", "", "")
	stdin := fs.Bool("password-stdin", false, "")
	got, err := parseFlags(fs, []string{"bob", "--password-stdin", "--data", "/x", "--", "-literal"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []string{"bob", "-literal"}) || !*stdin || *data != "/x" {
		t.Fatalf("got %v stdin=%v data=%q", got, *stdin, *data)
	}
	if _, err := parseFlags(fs, []string{"--nope"}); err == nil {
		t.Fatal("unknown flag accepted")
	}
}

func TestParseSize(t *testing.T) {
	tests := []struct {
		in   string
		w, h int
		ok   bool
	}{
		{"150x44", 150, 44, true},
		{"80X24", 80, 24, true},
		{"10x10", 0, 0, false},
		{"150", 0, 0, false},
		{"ax44", 0, 0, false},
		{"5000x44", 0, 0, false},
	}
	for _, tt := range tests {
		w, h, err := parseSize(tt.in)
		if (err == nil) != tt.ok || w != tt.w || h != tt.h {
			t.Errorf("parseSize(%q) = %d, %d, %v", tt.in, w, h, err)
		}
	}
}

func TestParseCompat(t *testing.T) {
	tests := []struct {
		in   string
		want compat.Mode
		ok   bool
	}{
		{"auto", compat.Auto, true},
		{"blocks", compat.Blocks, true},
		{"ASCII", compat.ASCII, true},
		{"off", compat.Off, true},
		{"fancy", 0, false},
	}
	for _, tt := range tests {
		got, err := parseCompat(tt.in)
		if (err == nil) != tt.ok || (tt.ok && got != tt.want) {
			t.Errorf("parseCompat(%q) = %v, %v", tt.in, got, err)
		}
	}
}

func TestDefaultDataDir(t *testing.T) {
	config := func() (string, error) { return filepath.FromSlash("/home/u/.config"), nil }
	noConfig := func() (string, error) { return "", errors.New("no home") }
	env := func(v string) func(string) string {
		return func(k string) string {
			if k == "VIGIL_DATA" {
				return v
			}
			return ""
		}
	}
	abs := func(p string) string {
		a, _ := filepath.Abs(filepath.FromSlash(p))
		return a
	}
	tests := []struct {
		name    string
		getenv  func(string) string
		goos    string
		euid    int
		config  func() (string, error)
		want    string
		wantErr bool
	}{
		{"env wins", env("/srv/vigil"), "linux", 0, config, abs("/srv/vigil"), false},
		{"linux root", env(""), "linux", 0, config, "/var/lib/vigil", false},
		{"linux user", env(""), "linux", 1000, config, filepath.Join(filepath.FromSlash("/home/u/.config"), "vigil"), false},
		{"darwin root uses config", env(""), "darwin", 0, config, filepath.Join(filepath.FromSlash("/home/u/.config"), "vigil"), false},
		{"no config dir", env(""), "windows", -1, noConfig, "", true},
	}
	for _, tt := range tests {
		got, err := defaultDataDir(tt.getenv, tt.goos, tt.euid, tt.config)
		if (err != nil) != tt.wantErr || got != tt.want {
			t.Errorf("%s: got %q, %v; want %q", tt.name, got, err, tt.want)
		}
	}
}

func TestRandomPassword(t *testing.T) {
	seen := map[string]bool{}
	for range 50 {
		p, err := randomPassword(bootstrapPassLen)
		if err != nil {
			t.Fatal(err)
		}
		if len(p) != bootstrapPassLen {
			t.Fatalf("len = %d", len(p))
		}
		for _, r := range p {
			if !strings.ContainsRune(passwordAlphabet, r) {
				t.Fatalf("unexpected character %q in %q", r, p)
			}
		}
		if seen[p] {
			t.Fatalf("duplicate password %q", p)
		}
		seen[p] = true
	}
	if strings.ContainsAny(passwordAlphabet, "0O1lI") {
		t.Fatal("alphabet contains ambiguous characters")
	}
}

func TestPrintFirstStart(t *testing.T) {
	var out bytes.Buffer
	printFirstStart(&out, "s3cretpassword", "ssh-ed25519 AAAAkey vigil@manager", ":2222")
	text := out.String()
	for _, want := range []string{"s3cretpassword", "-p 2222", "vigil user passwd admin", "ssh-ed25519 AAAAkey vigil@manager"} {
		if !strings.Contains(text, want) {
			t.Errorf("output lacks %q:\n%s", want, text)
		}
	}
	lines := strings.Split(text, "\n")
	width := len(lines[0])
	for _, l := range lines {
		if strings.HasPrefix(l, "|") && len(l) != width {
			t.Errorf("box line has width %d, want %d: %q", len(l), width, l)
		}
	}
}

func TestUserCommands(t *testing.T) {
	dir := t.TempDir()
	run := func(stdin string, args ...string) (string, error) {
		std, out, _ := testIO(stdin)
		err := dispatch(append(append([]string{"user"}, args...), "--data", dir), std)
		return out.String(), err
	}
	if _, err := run("password-one\n", "add", "bob", "--password-stdin"); err != nil {
		t.Fatal(err)
	}
	if _, err := run("password-one\n", "add", "bob", "--password-stdin"); err == nil {
		t.Fatal("adding an existing user succeeded")
	}
	if _, err := run("short\n", "add", "carol", "--password-stdin"); !errors.Is(err, auth.ErrPasswordTooShort) {
		t.Fatalf("short password: %v", err)
	}
	if _, err := run("pw-for-nobody\n", "passwd", "nobody", "--password-stdin"); err == nil {
		t.Fatal("passwd for unknown user succeeded")
	}
	if _, err := run("password-two\r\n", "passwd", "bob", "--password-stdin"); err != nil {
		t.Fatal(err)
	}
	if _, err := run("", "add", "alice"); err == nil {
		t.Fatal("interactive add without a terminal succeeded")
	}
	if _, err := run("x\n", "add", "bad name", "--password-stdin"); !errors.Is(err, auth.ErrInvalidName) {
		t.Fatalf("bad name: %v", err)
	}

	a, err := auth.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !a.CheckPassword("bob", "password-two") {
		t.Fatal("passwd did not change the password")
	}

	out, err := run("", "list")
	if err != nil || out != "bob\n" {
		t.Fatalf("list = %q, %v", out, err)
	}
	if _, err := run("", "rm", "bob"); err != nil {
		t.Fatal(err)
	}
	if _, err := run("", "rm", "bob"); err == nil {
		t.Fatal("removing a missing user succeeded")
	}
	if _, err := run("", "frob"); err == nil {
		t.Fatal("unknown user command succeeded")
	}
}
