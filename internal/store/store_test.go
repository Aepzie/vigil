package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func openTemp(t *testing.T) (*Store, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "data")
	st, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return st, dir
}

func mustAdd(t *testing.T, st *Store, srv Server) Server {
	t.Helper()
	got, err := st.Add(srv)
	if err != nil {
		t.Fatalf("Add(%q): %v", srv.Name, err)
	}
	return got
}

func TestOpenCreatesDirWithDefaults(t *testing.T) {
	st, dir := openTemp(t)
	fi, err := os.Stat(dir)
	if err != nil || !fi.IsDir() {
		t.Fatalf("data dir not created: %v", err)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o700 {
		t.Errorf("dir mode = %v, want 0700", fi.Mode().Perm())
	}
	if got := st.Servers(); len(got) != 0 {
		t.Errorf("Servers() = %v, want empty", got)
	}
	if st.Settings() != DefaultSettings() {
		t.Errorf("Settings() = %+v, want defaults", st.Settings())
	}
	if st.Version() != 0 {
		t.Errorf("Version() = %d, want 0", st.Version())
	}
	if _, err := Open(""); err == nil {
		t.Error("Open(\"\") succeeded, want error")
	}
}

func TestCRUD(t *testing.T) {
	st, _ := openTemp(t)
	clock := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	st.now = func() time.Time { return clock }

	added := mustAdd(t, st, Server{ID: "ignored", Name: " web-01 ", Host: "[::1]", User: "deploy", Tags: []string{"prod", "PROD"}})
	if len(added.ID) != 16 || added.ID == "ignored" {
		t.Errorf("ID = %q, want 16 hex chars", added.ID)
	}
	if added.Name != "web-01" || added.Host != "::1" || added.Port != 22 || len(added.Tags) != 1 {
		t.Errorf("Add did not normalize: %+v", added)
	}
	if !added.CreatedAt.Equal(clock) || !added.UpdatedAt.Equal(clock) {
		t.Errorf("timestamps = %v/%v, want %v", added.CreatedAt, added.UpdatedAt, clock)
	}
	if st.Version() != 1 {
		t.Errorf("Version() = %d, want 1", st.Version())
	}

	got, ok := st.Server(added.ID)
	if !ok || got.Name != "web-01" {
		t.Fatalf("Server(%q) = %+v, %v", added.ID, got, ok)
	}
	got.Tags[0] = "mutated"
	if again, _ := st.Server(added.ID); again.Tags[0] != "prod" {
		t.Error("Server() returned an alias of internal tags")
	}

	clock = clock.Add(time.Hour)
	upd := added
	upd.Name = "web-one"
	upd.CreatedAt = time.Time{}
	if err := st.Update(upd); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, _ = st.Server(added.ID)
	if got.Name != "web-one" || !got.CreatedAt.Equal(added.CreatedAt) || !got.UpdatedAt.Equal(clock) {
		t.Errorf("after Update: %+v", got)
	}
	if st.Version() != 2 {
		t.Errorf("Version() = %d, want 2", st.Version())
	}

	if err := st.Update(Server{ID: "nope", Name: "x", Host: "h", User: "u"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("Update(unknown) = %v, want ErrNotFound", err)
	}
	bad := got
	bad.Host = "foo bar"
	if err := st.Update(bad); err == nil || !strings.Contains(err.Error(), "host") {
		t.Errorf("Update(invalid) = %v, want host error", err)
	}

	if err := st.Delete(added.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok := st.Server(added.ID); ok {
		t.Error("server still present after Delete")
	}
	if err := st.Delete(added.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete(again) = %v, want ErrNotFound", err)
	}
	if st.Version() != 3 {
		t.Errorf("Version() = %d, want 3", st.Version())
	}
}

func TestServersSortedCaseInsensitive(t *testing.T) {
	st := NewMemory()
	for _, name := range []string{"zeta", "Alpha", "beta", "Gamma"} {
		mustAdd(t, st, Server{Name: name, Host: "example.com", User: "u"})
	}
	var names []string
	for _, srv := range st.Servers() {
		names = append(names, srv.Name)
	}
	if got := strings.Join(names, ","); got != "Alpha,beta,Gamma,zeta" {
		t.Errorf("order = %s", got)
	}
}

func TestDuplicateNames(t *testing.T) {
	st := NewMemory()
	a := mustAdd(t, st, Server{Name: "Web", Host: "a.example", User: "u"})
	b := mustAdd(t, st, Server{Name: "db", Host: "b.example", User: "u"})

	_, err := st.Add(Server{Name: "WEB", Host: "c.example", User: "u"})
	if !errors.Is(err, ErrDuplicateName) || !strings.HasPrefix(err.Error(), "name: ") {
		t.Errorf("Add(dup) = %v, want ErrDuplicateName with name prefix", err)
	}
	b.Name = " web "
	if err := st.Update(b); !errors.Is(err, ErrDuplicateName) {
		t.Errorf("Update(dup) = %v, want ErrDuplicateName", err)
	}
	a.Name = "WEB"
	if err := st.Update(a); err != nil {
		t.Errorf("renaming to own name in other case: %v", err)
	}
	if v := st.Version(); v != 3 {
		t.Errorf("Version() = %d, want 3 (failed mutations must not count)", v)
	}
}

func TestPersistenceRoundTrip(t *testing.T) {
	st, dir := openTemp(t)
	srv := mustAdd(t, st, Server{Name: "db-primary", Host: "10.0.0.5", Port: 2222, User: "postgres", KeyPath: "db_ed25519", Tags: []string{"prod", "db"}})
	mustAdd(t, st, Server{Name: "cache", Host: "cache.example.internal", User: "ops"})
	set := DefaultSettings()
	set.PollInterval = 5 * time.Second
	set.OfflineGrace = 0
	set.CPUWarn = 70.5
	set.Compat = "ascii"
	if err := st.SaveSettings(set); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}

	for _, name := range []string{serversFile, settingsFile} {
		path := filepath.Join(dir, name)
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
			t.Errorf("%s mode = %v, want 0600", name, fi.Mode().Perm())
		}
		b, _ := os.ReadFile(path)
		var env struct {
			Version int `json:"version"`
		}
		if err := json.Unmarshal(b, &env); err != nil || env.Version != 1 {
			t.Errorf("%s envelope version = %d, %v", name, env.Version, err)
		}
		if !strings.Contains(string(b), "\n  ") {
			t.Errorf("%s is not pretty-printed", name)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(dir, settingsFile)); !strings.Contains(string(b), `"poll_interval": "5s"`) {
		t.Errorf("settings.json durations not human readable:\n%s", b)
	}

	re, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if re.Settings() != set {
		t.Errorf("settings = %+v, want %+v", re.Settings(), set)
	}
	got, ok := re.Server(srv.ID)
	if !ok {
		t.Fatal("server lost after reopen")
	}
	if got.Name != srv.Name || got.Port != 2222 || got.KeyPath != "db_ed25519" || strings.Join(got.Tags, ",") != "prod,db" ||
		!got.CreatedAt.Equal(srv.CreatedAt) || !got.UpdatedAt.Equal(srv.UpdatedAt) {
		t.Errorf("round trip mismatch:\n got %+v\nwant %+v", got, srv)
	}
	if names := re.Servers(); len(names) != 2 || names[0].Name != "cache" {
		t.Errorf("Servers() after reopen = %+v", names)
	}

	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Errorf("leftover temp file %s", e.Name())
		}
	}
}

func TestOpenMergesMissingSettings(t *testing.T) {
	dir := t.TempDir()
	content := `{"version": 1, "settings": {"poll_interval": 7000000000, "cpu_warn": 60, "offline_grace": "0s"}}`
	if err := os.WriteFile(filepath.Join(dir, settingsFile), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	want := DefaultSettings()
	want.PollInterval = 7 * time.Second
	want.CPUWarn = 60
	want.OfflineGrace = 0
	if got := st.Settings(); got != want {
		t.Errorf("Settings() = %+v, want %+v", got, want)
	}
}

func TestOpenRejectsBadFiles(t *testing.T) {
	tests := []struct {
		name    string
		file    string
		content string
		wantErr string
	}{
		{"corrupt servers", serversFile, `{"version": 1, "servers": [`, "corrupt"},
		{"empty servers", serversFile, "", "empty or corrupt"},
		{"future version", serversFile, `{"version": 2, "servers": []}`, "unsupported version 2"},
		{"invalid server", serversFile, `{"version":1,"servers":[{"id":"a","name":"x","host":"foo bar","user":"u"}]}`, "not a valid hostname"},
		{"missing id", serversFile, `{"version":1,"servers":[{"name":"x","host":"h","user":"u"}]}`, "has no id"},
		{"duplicate id", serversFile, `{"version":1,"servers":[{"id":"a","name":"x","host":"h","user":"u"},{"id":"a","name":"y","host":"h","user":"u"}]}`, "duplicate server id"},
		{"duplicate name", serversFile, `{"version":1,"servers":[{"id":"a","name":"x","host":"h","user":"u"},{"id":"b","name":"X","host":"h","user":"u"}]}`, "duplicate server name"},
		{"corrupt settings", settingsFile, `not json`, "corrupt"},
		{"bad duration", settingsFile, `{"version":1,"settings":{"poll_interval":"soon"}}`, "corrupt"},
		{"invalid settings", settingsFile, `{"version":1,"settings":{"compat":"fancy"}}`, "compat"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, tt.file)
			if err := os.WriteFile(path, []byte(tt.content), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := Open(dir)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Open() = %v, want error containing %q", err, tt.wantErr)
			}
			if b, _ := os.ReadFile(path); string(b) != tt.content {
				t.Error("bad file was modified")
			}
		})
	}
}

func TestWriteFailureKeepsState(t *testing.T) {
	st, dir := openTemp(t)
	mustAdd(t, st, Server{Name: "keep", Host: "h.example", User: "u"})
	set := DefaultSettings()
	before := st.Version()

	for _, name := range []string{serversFile, settingsFile} {
		path := filepath.Join(dir, name)
		_ = os.Remove(path)
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "blocker"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := st.Add(Server{Name: "new", Host: "n.example", User: "u"}); err == nil {
		t.Fatal("Add succeeded although servers.json cannot be replaced")
	}
	set.CPUWarn = 50
	if err := st.SaveSettings(set); err == nil {
		t.Fatal("SaveSettings succeeded although settings.json cannot be replaced")
	}
	if got := st.Servers(); len(got) != 1 || got[0].Name != "keep" {
		t.Errorf("in-memory servers changed: %+v", got)
	}
	if st.Settings().CPUWarn != DefaultSettings().CPUWarn {
		t.Error("in-memory settings changed after failed write")
	}
	if st.Version() != before {
		t.Errorf("Version() = %d, want %d", st.Version(), before)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Errorf("leftover temp file %s", e.Name())
		}
	}
}

func TestSaveSettingsValidates(t *testing.T) {
	st := NewMemory()
	set := DefaultSettings()
	set.CPUWarn = 99
	if err := st.SaveSettings(set); err == nil || !strings.HasPrefix(err.Error(), "cpu_warn: ") {
		t.Fatalf("SaveSettings(invalid) = %v", err)
	}
	if st.Settings() != DefaultSettings() || st.Version() != 0 {
		t.Error("invalid settings were applied")
	}
}

func TestConcurrentAccess(t *testing.T) {
	st, _ := openTemp(t)
	const writers, perWriter = 4, 10
	var wg sync.WaitGroup
	stop := make(chan struct{})

	for range 4 {
		wg.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
				}
				for _, srv := range st.Servers() {
					_, _ = st.Server(srv.ID)
				}
				_ = st.Settings()
				_ = st.Version()
			}
		})
	}

	var writersWG sync.WaitGroup
	for w := range writers {
		writersWG.Go(func() {
			for i := range perWriter {
				srv, err := st.Add(Server{Name: fmt.Sprintf("srv-%d-%d", w, i), Host: "h.example", User: "u"})
				if err != nil {
					t.Errorf("Add: %v", err)
					return
				}
				srv.Tags = []string{"t"}
				if err := st.Update(srv); err != nil {
					t.Errorf("Update: %v", err)
				}
				if i%3 == 0 {
					if err := st.Delete(srv.ID); err != nil {
						t.Errorf("Delete: %v", err)
					}
				}
			}
		})
	}
	writersWG.Wait()
	close(stop)
	wg.Wait()

	deleted := writers * ((perWriter + 2) / 3)
	if got := len(st.Servers()); got != writers*perWriter-deleted {
		t.Errorf("len(Servers()) = %d, want %d", got, writers*perWriter-deleted)
	}
	re, err := Open(st.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if len(re.Servers()) != len(st.Servers()) {
		t.Errorf("persisted %d servers, want %d", len(re.Servers()), len(st.Servers()))
	}
}
