package main

import (
	"runtime/debug"
	"testing"
)

func TestBuildVersion(t *testing.T) {
	rev := []debug.BuildSetting{{Key: "vcs.revision", Value: "65ac643cd3c8aa"}}
	dirty := append(rev, debug.BuildSetting{Key: "vcs.modified", Value: "true"})
	tests := []struct {
		name     string
		mod      string
		settings []debug.BuildSetting
		want     string
	}{
		{"release tag", "v1.2.3", rev, "v1.2.3"},
		{"prerelease tag", "v1.2.3-rc.1", nil, "v1.2.3-rc.1"},
		{"pseudo version uses vcs revision", "v0.0.0-20261108035403-65ac643cd3c8", rev, "dev-65ac643"},
		{"pseudo version dirty", "v0.0.0-20261108035403-65ac643cd3c8+dirty", dirty, "dev-65ac643+dirty"},
		{"pseudo version after tag", "v1.2.4-0.20261108035403-65ac643cd3c8", nil, "dev-65ac643"},
		{"devel without vcs", "(devel)", nil, "dev"},
		{"devel with vcs", "(devel)", dirty, "dev-65ac643+dirty"},
		{"empty", "", nil, "dev"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := buildVersion(tt.mod, tt.settings); got != tt.want {
				t.Errorf("buildVersion(%q) = %q, want %q", tt.mod, got, tt.want)
			}
		})
	}
}
