package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLegacyKeyPathStillLoads(t *testing.T) {
	dir := t.TempDir()
	content := `{"version":1,"servers":[{"id":"a","name":"db","host":"10.0.0.5","port":22,"user":"u","key_path":"/root/.ssh/id_ed25519"}]}`
	if err := os.WriteFile(filepath.Join(dir, serversFile), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := Open(dir)
	if err != nil {
		t.Fatalf("Open with a legacy key path: %v", err)
	}
	srv, ok := st.Server("a")
	if !ok || srv.KeyPath != "/root/.ssh/id_ed25519" {
		t.Fatalf("Server = %+v, %v", srv, ok)
	}
	var fe *FieldError
	if err := st.Update(srv); !errors.As(err, &fe) || fe.Field != "key_path" {
		t.Fatalf("Update keeping the legacy key path = %v, want a key_path error", err)
	}
	srv.KeyPath = "db_ed25519"
	if err := st.Update(srv); err != nil {
		t.Fatalf("Update with a key name: %v", err)
	}
}

func TestValidateKeyName(t *testing.T) {
	for _, name := range []string{"id_ed25519", "a", "9.key", strings.Repeat("x", MaxKeyNameLen)} {
		if err := ValidateKeyName(name); err != nil {
			t.Errorf("ValidateKeyName(%q) = %v, want nil", name, err)
		}
	}
	for _, name := range []string{"", "..", ".", "-key", "_key", "a/b", `a\b`, "/etc/passwd", "~root", "a b", strings.Repeat("x", MaxKeyNameLen+1)} {
		if err := ValidateKeyName(name); err == nil {
			t.Errorf("ValidateKeyName(%q) = nil, want error", name)
		}
	}
}

func validServer() Server {
	return Server{Name: "web-01", Host: "10.0.0.1", Port: 22, User: "deploy"}
}

func TestServerValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Server)
		field   string
		wantMsg string
	}{
		{name: "valid", mutate: func(*Server) {}},
		{name: "zero port defaults", mutate: func(s *Server) { s.Port = 0 }},
		{name: "hostname", mutate: func(s *Server) { s.Host = "db-1.example.internal" }},
		{name: "hostname trailing dot", mutate: func(s *Server) { s.Host = "example.com." }},
		{name: "uppercase hostname", mutate: func(s *Server) { s.Host = "Web.Example.COM" }},
		{name: "ipv6", mutate: func(s *Server) { s.Host = "2001:db8::1" }},
		{name: "bracketed ipv6", mutate: func(s *Server) { s.Host = "[::1]" }},
		{name: "unicode name", mutate: func(s *Server) { s.Name = "Ünïcødé ☃" }},
		{name: "48 char name", mutate: func(s *Server) { s.Name = strings.Repeat("a", 48) }},
		{name: "uppercase user", mutate: func(s *Server) { s.User = "Admin" }},
		{name: "underscore user", mutate: func(s *Server) { s.User = "_svc.backup-1" }},
		{name: "key name", mutate: func(s *Server) { s.KeyPath = "id_ed25519" }},
		{name: "key name with dots and dashes", mutate: func(s *Server) { s.KeyPath = "db-01.prod_rsa.key" }},
		{name: "key name 64 chars", mutate: func(s *Server) { s.KeyPath = strings.Repeat("k", 64) }},
		{name: "key name trimmed", mutate: func(s *Server) { s.KeyPath = "  web  " }},
		{name: "tags", mutate: func(s *Server) { s.Tags = []string{"prod", "db", "eu-west.1", "a_b"} }},
		{name: "duplicate tags collapse", mutate: func(s *Server) { s.Tags = []string{"a", "A", "b", "c", "d", "e", "f", "g", "h", "h"} }},

		{name: "empty name", mutate: func(s *Server) { s.Name = "  " }, field: "name", wantMsg: "must not be empty"},
		{name: "long name", mutate: func(s *Server) { s.Name = strings.Repeat("a", 49) }, field: "name", wantMsg: "at most 48"},
		{name: "control in name", mutate: func(s *Server) { s.Name = "web\x1b[31m" }, field: "name", wantMsg: "control"},
		{name: "format char in name", mutate: func(s *Server) { s.Name = "web\u202e01" }, field: "name", wantMsg: "control"},
		{name: "invalid utf8 name", mutate: func(s *Server) { s.Name = "web\xff" }, field: "name", wantMsg: "UTF-8"},
		{name: "empty host", mutate: func(s *Server) { s.Host = "" }, field: "host", wantMsg: "must not be empty"},
		{name: "host with space", mutate: func(s *Server) { s.Host = "foo bar" }, field: "host", wantMsg: `"foo bar" is not a valid hostname or IP`},
		{name: "host underscore", mutate: func(s *Server) { s.Host = "foo_bar.com" }, field: "host", wantMsg: "not a valid hostname"},
		{name: "host leading dash", mutate: func(s *Server) { s.Host = "-foo.com" }, field: "host", wantMsg: "not a valid hostname"},
		{name: "host empty label", mutate: func(s *Server) { s.Host = "foo..com" }, field: "host", wantMsg: "not a valid hostname"},
		{name: "host long label", mutate: func(s *Server) { s.Host = strings.Repeat("a", 64) + ".com" }, field: "host", wantMsg: "not a valid hostname"},
		{name: "host too long", mutate: func(s *Server) { s.Host = strings.Repeat("abcdefghi.", 26) + "com" }, field: "host", wantMsg: "not a valid hostname"},
		{name: "bad ipv4", mutate: func(s *Server) { s.Host = "999.1.1.1" }, field: "host", wantMsg: "not a valid hostname"},
		{name: "host with port", mutate: func(s *Server) { s.Host = "example.com:22" }, field: "host", wantMsg: "not a valid hostname"},
		{name: "negative port", mutate: func(s *Server) { s.Port = -1 }, field: "port", wantMsg: "out of range"},
		{name: "port too high", mutate: func(s *Server) { s.Port = 65536 }, field: "port", wantMsg: "out of range"},
		{name: "empty user", mutate: func(s *Server) { s.User = "" }, field: "user", wantMsg: "must not be empty"},
		{name: "user digit first", mutate: func(s *Server) { s.User = "1abc" }, field: "user", wantMsg: "not a valid user name"},
		{name: "user with space", mutate: func(s *Server) { s.User = "a b" }, field: "user", wantMsg: "not a valid user name"},
		{name: "user too long", mutate: func(s *Server) { s.User = strings.Repeat("a", 33) }, field: "user", wantMsg: "not a valid user name"},
		{name: "key absolute path", mutate: func(s *Server) { s.KeyPath = "/root/.ssh/id_ed25519" }, field: "key_path", wantMsg: "not a valid key name"},
		{name: "key windows path", mutate: func(s *Server) { s.KeyPath = `C:\keys\id_ed25519` }, field: "key_path", wantMsg: "not a valid key name"},
		{name: "key home path", mutate: func(s *Server) { s.KeyPath = "~/.ssh/id_ed25519" }, field: "key_path", wantMsg: "not a valid key name"},
		{name: "key tilde", mutate: func(s *Server) { s.KeyPath = "~" }, field: "key_path", wantMsg: "not a valid key name"},
		{name: "key parent", mutate: func(s *Server) { s.KeyPath = ".." }, field: "key_path", wantMsg: "not a valid key name"},
		{name: "key dot", mutate: func(s *Server) { s.KeyPath = "." }, field: "key_path", wantMsg: "not a valid key name"},
		{name: "key traversal", mutate: func(s *Server) { s.KeyPath = "../id_ed25519" }, field: "key_path", wantMsg: "not a valid key name"},
		{name: "key subdir", mutate: func(s *Server) { s.KeyPath = "sub/key" }, field: "key_path", wantMsg: "not a valid key name"},
		{name: "key backslash", mutate: func(s *Server) { s.KeyPath = `..\key` }, field: "key_path", wantMsg: "not a valid key name"},
		{name: "key hidden", mutate: func(s *Server) { s.KeyPath = ".key" }, field: "key_path", wantMsg: "not a valid key name"},
		{name: "key space", mutate: func(s *Server) { s.KeyPath = "my key" }, field: "key_path", wantMsg: "not a valid key name"},
		{name: "key unicode", mutate: func(s *Server) { s.KeyPath = "ключ" }, field: "key_path", wantMsg: "not a valid key name"},
		{name: "key control", mutate: func(s *Server) { s.KeyPath = "a\nb" }, field: "key_path", wantMsg: "not a valid key name"},
		{name: "key nul", mutate: func(s *Server) { s.KeyPath = "a\x00b" }, field: "key_path", wantMsg: "not a valid key name"},
		{name: "key too long", mutate: func(s *Server) { s.KeyPath = strings.Repeat("a", 65) }, field: "key_path", wantMsg: "at most 64"},
		{name: "too many tags", mutate: func(s *Server) { s.Tags = strings.Split("a b c d e f g h i", " ") }, field: "tags", wantMsg: "at most 8"},
		{name: "bad tag chars", mutate: func(s *Server) { s.Tags = []string{"foo bar"} }, field: "tags", wantMsg: `"foo bar"`},
		{name: "long tag", mutate: func(s *Server) { s.Tags = []string{strings.Repeat("t", 25)} }, field: "tags", wantMsg: "1–24"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := validServer()
			tt.mutate(&srv)
			err := srv.Validate()
			if tt.field == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			var fe *FieldError
			if !errors.As(err, &fe) {
				t.Fatalf("Validate() = %v, want *FieldError", err)
			}
			if fe.Field != tt.field {
				t.Errorf("field = %q, want %q", fe.Field, tt.field)
			}
			if !strings.Contains(err.Error(), tt.wantMsg) || !strings.HasPrefix(err.Error(), tt.field+": ") {
				t.Errorf("error = %q, want prefix %q and to contain %q", err, tt.field+": ", tt.wantMsg)
			}
		})
	}
}

func TestServerNormalized(t *testing.T) {
	got := Server{
		Name: "  web  ",
		Host: " [2001:DB8::0001] ",
		User: " deploy ",
		Tags: []string{" prod ", "", "Prod", "db"},
	}.Normalized()
	want := Server{Name: "web", Host: "2001:db8::1", Port: DefaultPort, User: "deploy", Tags: []string{"prod", "db"}}
	if got.Name != want.Name || got.Host != want.Host || got.Port != want.Port || got.User != want.User ||
		strings.Join(got.Tags, ",") != strings.Join(want.Tags, ",") {
		t.Errorf("Normalized() = %+v, want %+v", got, want)
	}
}

func TestServerAddr(t *testing.T) {
	tests := []struct {
		host string
		port int
		want string
	}{
		{"10.0.0.1", 22, "10.0.0.1:22"},
		{"example.com", 0, "example.com:22"},
		{"::1", 2222, "[::1]:2222"},
		{"[::1]", 2222, "[::1]:2222"},
		{"fe80::1%eth0", 22, "[fe80::1%eth0]:22"},
	}
	for _, tt := range tests {
		if got := (Server{Host: tt.host, Port: tt.port}).Addr(); got != tt.want {
			t.Errorf("Addr(%q, %d) = %q, want %q", tt.host, tt.port, got, tt.want)
		}
	}
}
