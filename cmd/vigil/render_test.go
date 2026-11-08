package main

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderSVGToFile(t *testing.T) {
	out := filepath.Join(t.TempDir(), "detail.svg")
	std, stdout, _ := testIO("")
	if err := dispatch([]string{"render", "--size", "80x24", "--tab", "Detail", "--format", "svg", "--out", out}, std); err != nil {
		t.Fatal(err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout not empty with --out: %q", stdout.String())
	}
	doc, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(doc, []byte("<svg ")) {
		t.Fatalf("not an SVG: %.60q", doc)
	}
	if !bytes.Contains(doc, []byte("<title>vigil — detail</title>")) {
		t.Fatal("SVG lacks the tab title")
	}
	dec := xml.NewDecoder(bytes.NewReader(doc))
	for {
		_, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("malformed SVG: %v", err)
		}
	}
}

func TestRenderSVGStdout(t *testing.T) {
	std, stdout, _ := testIO("")
	if err := dispatch([]string{"render", "--size", "80x24", "--format", "SVG"}, std); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(stdout.String(), "<svg ") || !strings.Contains(stdout.String(), "vigil — overview") {
		t.Fatalf("stdout = %.80q", stdout.String())
	}
}

func TestRenderANSIToFile(t *testing.T) {
	out := filepath.Join(t.TempDir(), "frame.ans")
	std, _, _ := testIO("")
	if err := dispatch([]string{"render", "--size", "80x24", "--out", out}, std); err != nil {
		t.Fatal(err)
	}
	frame, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasSuffix(frame, []byte("\x1b[0m\n")) || bytes.Count(frame, []byte("\n")) != 24 {
		t.Fatalf("unexpected ANSI frame file (%d bytes)", len(frame))
	}
}

func TestRenderBadFormat(t *testing.T) {
	std, _, _ := testIO("")
	if err := dispatch([]string{"render", "--format", "png"}, std); err == nil {
		t.Fatal("unknown format accepted")
	}
}
