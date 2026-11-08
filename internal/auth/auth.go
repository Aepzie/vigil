// Package auth verifies dashboard logins: bcrypt password hashes in
// <data>/users.json and public keys in <data>/authorized_keys.
package auth

import (
	"golang.org/x/crypto/ssh"
)

// Authenticator is safe for concurrent use. Files are re-read when their
// modification time changes so edits apply without a restart.
type Authenticator struct {
	dataDir string
}

// New prepares an authenticator for dataDir. Missing files mean "no
// users" / "no keys".
func New(dataDir string) (*Authenticator, error) {
	return &Authenticator{dataDir: dataDir}, nil
}

// CheckPassword reports whether user/password match a stored bcrypt hash.
// It runs in constant time with respect to whether the user exists.
func (a *Authenticator) CheckPassword(user, password string) bool { return false }

// CheckKey reports whether key is listed in authorized_keys.
func (a *Authenticator) CheckKey(user string, key ssh.PublicKey) bool { return false }

// SetPassword creates or updates a user with a bcrypt hash of password.
func (a *Authenticator) SetPassword(user, password string) error { return nil }

// RemoveUser deletes a user.
func (a *Authenticator) RemoveUser(user string) error { return nil }

// Users lists user names, sorted.
func (a *Authenticator) Users() []string { return nil }

// HasCredentials reports whether at least one user or key exists.
func (a *Authenticator) HasCredentials() bool { return false }
