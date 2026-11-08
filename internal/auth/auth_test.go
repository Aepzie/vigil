package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
	"golang.org/x/crypto/ssh"
)

func TestMain(m *testing.M) {
	cost = bcrypt.MinCost
	os.Exit(m.Run())
}

func newKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	k, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func authorizedLine(k ssh.PublicKey, comment string) string {
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(k))) + " " + comment
}

// touchLater writes a file and bumps its mtime so change detection works
// on filesystems with coarse timestamps.
func touchLater(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
}

func TestValidateName(t *testing.T) {
	tests := []struct {
		name string
		ok   bool
	}{
		{"admin", true},
		{"a", true},
		{"ops.team_1-x", true},
		{strings.Repeat("a", 32), true},
		{strings.Repeat("a", 33), false},
		{"", false},
		{"with space", false},
		{"root@host", false},
		{"ünï", false},
		{"../etc", false},
		{"a\nb", false},
	}
	for _, tt := range tests {
		if err := ValidateName(tt.name); (err == nil) != tt.ok {
			t.Errorf("ValidateName(%q) = %v, want ok=%v", tt.name, err, tt.ok)
		}
	}
}

func TestValidatePassword(t *testing.T) {
	tests := []struct {
		pw   string
		want error
	}{
		{"short", ErrPasswordTooShort},
		{"12345678", nil},
		{strings.Repeat("x", MaxPasswordLen), nil},
		{strings.Repeat("x", MaxPasswordLen+1), ErrPasswordTooLong},
	}
	for _, tt := range tests {
		if err := ValidatePassword(tt.pw); !errors.Is(err, tt.want) {
			t.Errorf("ValidatePassword(len %d) = %v, want %v", len(tt.pw), err, tt.want)
		}
	}
}

func TestPasswordLifecycle(t *testing.T) {
	dir := t.TempDir()
	a, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if a.HasCredentials() {
		t.Fatal("empty data dir has credentials")
	}
	if err := a.SetPassword("admin", "correct horse"); err != nil {
		t.Fatal(err)
	}
	if err := a.SetPassword("bob", "battery staple"); err != nil {
		t.Fatal(err)
	}
	if err := a.SetPassword("bad name", "whatever123"); !errors.Is(err, ErrInvalidName) {
		t.Fatalf("SetPassword(bad name) = %v", err)
	}
	if !a.HasCredentials() {
		t.Fatal("HasCredentials = false after SetPassword")
	}
	if got := a.Users(); !slices.Equal(got, []string{"admin", "bob"}) {
		t.Fatalf("Users = %v", got)
	}

	checks := []struct {
		user, pw string
		want     bool
	}{
		{"admin", "correct horse", true},
		{"admin", "battery staple", false},
		{"bob", "battery staple", true},
		{"nobody", "correct horse", false},
		{"", "", false},
	}
	for _, c := range checks {
		if got := a.CheckPassword(c.user, c.pw); got != c.want {
			t.Errorf("CheckPassword(%q, %q) = %v, want %v", c.user, c.pw, got, c.want)
		}
	}

	if err := a.SetPassword("admin", "new password"); err != nil {
		t.Fatal(err)
	}
	if a.CheckPassword("admin", "correct horse") || !a.CheckPassword("admin", "new password") {
		t.Fatal("password change not effective")
	}

	if err := a.RemoveUser("bob"); err != nil {
		t.Fatal(err)
	}
	if err := a.RemoveUser("bob"); !errors.Is(err, ErrUnknownUser) {
		t.Fatalf("second RemoveUser = %v", err)
	}
	if a.CheckPassword("bob", "battery staple") {
		t.Fatal("removed user can still log in")
	}

	path := filepath.Join(dir, UsersFile)
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Errorf("users.json mode = %v, want 0600", fi.Mode().Perm())
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc usersDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Version != 1 || len(doc.Users) != 1 || doc.Users[0].Name != "admin" || !strings.HasPrefix(doc.Users[0].Hash, "$2a$") {
		t.Fatalf("unexpected users.json: %s", raw)
	}
	leftovers, _ := filepath.Glob(filepath.Join(dir, ".*tmp*"))
	if len(leftovers) > 0 {
		t.Errorf("temporary files left behind: %v", leftovers)
	}
}

func TestUsersReloadOnChange(t *testing.T) {
	dir := t.TempDir()
	a, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if a.CheckPassword("carol", "secret-pass") {
		t.Fatal("unexpected login")
	}
	b, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.SetPassword("carol", "secret-pass"); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(filepath.Join(dir, UsersFile), later, later); err != nil {
		t.Fatal(err)
	}
	if !a.CheckPassword("carol", "secret-pass") {
		t.Fatal("change by another writer not picked up")
	}
}

func TestMalformedUsersFile(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{"not json", "{"},
		{"bad version", `{"version":2,"users":[]}`},
		{"bad name", `{"version":1,"users":[{"name":"a b","hash":"$2a$04$abcdefghijklmnopqrstuuJ0b6mVQ2sQvOqgG8m8bS0gbT6JHqK2y"}]}`},
		{"bad hash", `{"version":1,"users":[{"name":"admin","hash":"plain"}]}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, UsersFile), []byte(tt.content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := New(dir); err == nil {
				t.Fatal("New accepted a malformed users.json")
			}
		})
	}
}

func TestBrokenFileFailsClosed(t *testing.T) {
	dir := t.TempDir()
	a, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.SetPassword("admin", "password1"); err != nil {
		t.Fatal(err)
	}
	touchLater(t, filepath.Join(dir, UsersFile), "{broken")
	if a.CheckPassword("admin", "password1") {
		t.Fatal("login accepted with broken users.json")
	}
	if a.Err() == nil {
		t.Fatal("Err() = nil for broken users.json")
	}
	if err := a.SetPassword("admin", "password2"); err == nil {
		t.Fatal("SetPassword overwrote a broken users.json")
	}
}

func TestCheckKey(t *testing.T) {
	dir := t.TempDir()
	allowed, withOptions, unknown, added := newKey(t), newKey(t), newKey(t), newKey(t)
	content := strings.Join([]string{
		"# managed by hand",
		"",
		authorizedLine(allowed, "alice@laptop"),
		`from="10.0.0.1" ` + authorizedLine(withOptions, "restricted"),
		"ssh-ed25519 not-base64!!",
	}, "\n")
	path := filepath.Join(dir, AuthorizedKeysFile)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		user string
		key  ssh.PublicKey
		want bool
	}{
		{"listed", "admin", allowed, true},
		{"listed any user", "someone-else", allowed, true},
		{"options ignored", "admin", withOptions, false},
		{"unknown", "admin", unknown, false},
		{"nil", "admin", nil, false},
	}
	for _, tt := range tests {
		if got := a.CheckKey(tt.user, tt.key); got != tt.want {
			t.Errorf("%s: CheckKey = %v, want %v", tt.name, got, tt.want)
		}
	}
	if !a.HasCredentials() {
		t.Fatal("HasCredentials = false with an authorized key")
	}

	touchLater(t, path, content+"\n"+authorizedLine(added, "new"))
	if !a.CheckKey("admin", added) {
		t.Fatal("authorized_keys change not picked up")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if a.CheckKey("admin", allowed) {
		t.Fatal("key still accepted after authorized_keys was removed")
	}
}

func TestParseAuthorizedKeys(t *testing.T) {
	k := newKey(t)
	tests := []struct {
		name  string
		input string
		want  int
	}{
		{"empty", "", 0},
		{"comment only", "# nothing", 0},
		{"one", authorizedLine(k, "c"), 1},
		{"crlf and blank", "\r\n" + authorizedLine(k, "c") + "\r\n\r\n", 1},
		{"restrict option", "restrict " + authorizedLine(k, "c"), 0},
		{"garbage", "hello world", 0},
	}
	for _, tt := range tests {
		if got := len(ParseAuthorizedKeys([]byte(tt.input))); got != tt.want {
			t.Errorf("%s: got %d keys, want %d", tt.name, got, tt.want)
		}
	}
}
