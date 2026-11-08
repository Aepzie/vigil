package auth

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func hashWithCost(t *testing.T, password string, hashCost int) string {
	t.Helper()
	h, err := bcrypt.GenerateFromPassword([]byte(password), hashCost)
	if err != nil {
		t.Fatal(err)
	}
	return string(h)
}

func writeUsers(t *testing.T, dir string, users map[string]string) {
	t.Helper()
	doc := usersDoc{Version: usersVersion}
	for name, hash := range users {
		doc.Users = append(doc.Users, userRecord{Name: name, Hash: hash})
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	touchLater(t, filepath.Join(dir, UsersFile), string(raw))
}

func storedCost(t *testing.T, dir, user string) int {
	t.Helper()
	users, err := readUsers(filepath.Join(dir, UsersFile))
	if err != nil {
		t.Fatal(err)
	}
	c, err := bcrypt.Cost(users[user])
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func withCost(t *testing.T, c int) {
	t.Helper()
	old := cost
	cost = c
	t.Cleanup(func() { cost = old })
}

func TestDummyHashFollowsStoredCost(t *testing.T) {
	dir := t.TempDir()
	writeUsers(t, dir, map[string]string{
		"low":  hashWithCost(t, "password1", bcrypt.MinCost),
		"high": hashWithCost(t, "password2", bcrypt.MinCost+2),
	})
	a, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if a.CheckPassword("nobody", "password1") {
		t.Fatal("unknown user accepted")
	}
	if c, err := bcrypt.Cost(a.dummyHash); err != nil || c != bcrypt.MinCost+2 {
		t.Fatalf("dummy hash cost = %d (%v), want the highest stored cost %d", c, err, bcrypt.MinCost+2)
	}

	writeUsers(t, dir, map[string]string{"low": hashWithCost(t, "password1", bcrypt.MinCost)})
	if a.CheckPassword("nobody", "password1") {
		t.Fatal("unknown user accepted")
	}
	if c, err := bcrypt.Cost(a.dummyHash); err != nil || c != cost {
		t.Fatalf("dummy hash cost = %d (%v), want the configured cost %d", c, err, cost)
	}
}

func TestLoginUpgradesWeakHash(t *testing.T) {
	withCost(t, bcrypt.MinCost+1)
	dir := t.TempDir()
	writeUsers(t, dir, map[string]string{
		"weak":   hashWithCost(t, "password1", bcrypt.MinCost),
		"strong": hashWithCost(t, "password2", bcrypt.MinCost+2),
	})
	a, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}

	if a.CheckPassword("weak", "wrong-password") {
		t.Fatal("wrong password accepted")
	}
	if got := storedCost(t, dir, "weak"); got != bcrypt.MinCost {
		t.Fatalf("failed login changed the hash cost to %d", got)
	}
	if !a.CheckPassword("weak", "password1") {
		t.Fatal("correct password rejected")
	}
	if got := storedCost(t, dir, "weak"); got != cost {
		t.Fatalf("hash cost after login = %d, want %d", got, cost)
	}
	if !a.CheckPassword("weak", "password1") {
		t.Fatal("upgraded hash rejects the password")
	}

	if !a.CheckPassword("strong", "password2") {
		t.Fatal("correct password rejected")
	}
	if got := storedCost(t, dir, "strong"); got != bcrypt.MinCost+2 {
		t.Fatalf("stronger hash was changed to cost %d", got)
	}
	if fi, err := os.Stat(filepath.Join(dir, UsersFile)); err != nil || fi.Size() == 0 {
		t.Fatalf("users.json damaged: %v", err)
	}
}
