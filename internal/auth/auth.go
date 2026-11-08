// Package auth verifies dashboard logins: bcrypt password hashes in
// <data>/users.json and public keys in <data>/authorized_keys.
//
// users.json has the form
//
//	{"version":1,"users":[{"name":"admin","hash":"$2a$12$..."}]}
//
// and is always written atomically with 0600 permissions.
//
// authorized_keys uses the OpenSSH format, one key per line. A listed key
// grants access under any user name: the SSH user name is only used for
// display. Lines with key options (from=, command=, restrict, ...) are
// ignored because vigil cannot enforce them, as are malformed lines and
// comments.
//
// On Unix both files must be owned by the user running vigil; users.json
// must not be accessible by group or others and authorized_keys must not
// be writable by them. A file failing these checks is not used (treated as
// empty) and the problem is reported by New and Err.
package auth

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
	"golang.org/x/crypto/ssh"

	"github.com/CraguS/vigil/internal/fsperm"
)

// File names inside the data directory.
const (
	UsersFile          = "users.json"
	AuthorizedKeysFile = "authorized_keys"
)

// BcryptCost is the work factor of newly created password hashes.
const BcryptCost = 12

// cost is the work factor actually used; tests lower it for speed.
var cost = BcryptCost

// Password length limits. bcrypt ignores everything after 72 bytes, so
// longer passwords are rejected instead of silently truncated.
const (
	MinPasswordLen = 8
	MaxPasswordLen = 72
)

const usersVersion = 1

var (
	// ErrInvalidName is returned for user names outside
	// ^[a-zA-Z0-9_.-]{1,32}$.
	ErrInvalidName = errors.New("invalid user name: use 1-32 characters from a-z A-Z 0-9 _ . -")
	// ErrUnknownUser is returned when removing a user that does not exist.
	ErrUnknownUser = errors.New("unknown user")
	// ErrPasswordTooShort is returned for passwords below MinPasswordLen.
	ErrPasswordTooShort = fmt.Errorf("password too short: use at least %d characters", MinPasswordLen)
	// ErrPasswordTooLong is returned for passwords above MaxPasswordLen
	// bytes.
	ErrPasswordTooLong = fmt.Errorf("password too long: use at most %d bytes", MaxPasswordLen)
)

var nameRE = regexp.MustCompile(`^[a-zA-Z0-9_.-]{1,32}$`)

// ValidateName reports whether name is an acceptable user name.
func ValidateName(name string) error {
	if !nameRE.MatchString(name) {
		return ErrInvalidName
	}
	return nil
}

// ValidatePassword checks the length limits of a new password.
func ValidatePassword(password string) error {
	switch {
	case len(password) < MinPasswordLen:
		return ErrPasswordTooShort
	case len(password) > MaxPasswordLen:
		return ErrPasswordTooLong
	}
	return nil
}

type userRecord struct {
	Name string `json:"name"`
	Hash string `json:"hash"`
}

type usersDoc struct {
	Version int          `json:"version"`
	Users   []userRecord `json:"users"`
}

// fileStamp identifies a version of a file on disk; a zero stamp means
// the file does not exist. The mode is part of it so that a chmod causes
// a re-read and with it a new permission check.
type fileStamp struct {
	exists  bool
	modTime time.Time
	size    int64
	mode    fs.FileMode
}

func stat(path string) (fileStamp, error) {
	fi, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return fileStamp{}, nil
	}
	if err != nil {
		return fileStamp{}, err
	}
	return fileStamp{exists: true, modTime: fi.ModTime(), size: fi.Size(), mode: fi.Mode()}, nil
}

// Authenticator is safe for concurrent use. Files are re-read when their
// modification time changes so edits apply without a restart.
type Authenticator struct {
	dataDir string

	mu         sync.Mutex
	users      map[string][]byte
	usersStamp fileStamp
	usersErr   error
	keys       [][]byte
	keysStamp  fileStamp
	keysErr    error

	dummyOnce sync.Once
	dummyHash []byte
}

// New prepares an authenticator for dataDir. Missing files mean "no
// users" / "no keys"; a malformed users.json is an error.
func New(dataDir string) (*Authenticator, error) {
	a := &Authenticator{dataDir: dataDir}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.refreshLocked()
	if a.usersErr != nil {
		return nil, a.usersErr
	}
	if a.keysErr != nil {
		return nil, a.keysErr
	}
	return a, nil
}

func (a *Authenticator) usersPath() string { return filepath.Join(a.dataDir, UsersFile) }
func (a *Authenticator) keysPath() string  { return filepath.Join(a.dataDir, AuthorizedKeysFile) }

// refreshLocked re-reads files whose stamp changed. A file that cannot be
// read or parsed yields an empty set (fail closed) and records the error.
func (a *Authenticator) refreshLocked() {
	if st, err := stat(a.usersPath()); err != nil {
		a.users, a.usersStamp, a.usersErr = nil, fileStamp{}, err
	} else if st != a.usersStamp || a.usersErr != nil {
		users, err := readUsers(a.usersPath())
		a.users, a.usersStamp, a.usersErr = users, st, err
	}
	if st, err := stat(a.keysPath()); err != nil {
		a.keys, a.keysStamp, a.keysErr = nil, fileStamp{}, err
	} else if st != a.keysStamp || a.keysErr != nil {
		keys, err := readKeys(a.keysPath())
		a.keys, a.keysStamp, a.keysErr = keys, st, err
	}
}

func readUsers(path string) (map[string][]byte, error) {
	err := fsperm.CheckPrivateFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string][]byte{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("refusing to use %s: %w", UsersFile, err)
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string][]byte{}, nil
	}
	if err != nil {
		return nil, err
	}
	var doc usersDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if doc.Version != usersVersion {
		return nil, fmt.Errorf("parse %s: unsupported version %d", path, doc.Version)
	}
	users := make(map[string][]byte, len(doc.Users))
	for _, u := range doc.Users {
		if ValidateName(u.Name) != nil {
			return nil, fmt.Errorf("parse %s: invalid user name %q", path, u.Name)
		}
		if _, err := bcrypt.Cost([]byte(u.Hash)); err != nil {
			return nil, fmt.Errorf("parse %s: user %q: %w", path, u.Name, err)
		}
		users[u.Name] = []byte(u.Hash)
	}
	return users, nil
}

func readKeys(path string) ([][]byte, error) {
	err := fsperm.CheckNotWritableByOthers(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("refusing to use %s: %w", AuthorizedKeysFile, err)
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return ParseAuthorizedKeys(raw), nil
}

// ParseAuthorizedKeys returns the wire encoding of every usable key in an
// authorized_keys file. Comments, blank lines, malformed lines and lines
// carrying options are skipped.
func ParseAuthorizedKeys(raw []byte) [][]byte {
	var keys [][]byte
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 || line[0] == '#' {
			continue
		}
		key, _, options, _, err := ssh.ParseAuthorizedKey(line)
		if err != nil || len(options) > 0 {
			continue
		}
		keys = append(keys, key.Marshal())
	}
	return keys
}

// dummy returns a valid hash compared against when a user does not exist,
// so failed logins take the same time either way.
func (a *Authenticator) dummy() []byte {
	a.dummyOnce.Do(func() {
		h, err := bcrypt.GenerateFromPassword([]byte(rand.Text()), cost)
		if err != nil {
			panic("auth: bcrypt: " + err.Error())
		}
		a.dummyHash = h
	})
	return a.dummyHash
}

// CheckPassword reports whether user/password match a stored bcrypt hash.
// It runs in constant time with respect to whether the user exists.
func (a *Authenticator) CheckPassword(user, password string) bool {
	a.mu.Lock()
	a.refreshLocked()
	hash, ok := a.users[user]
	a.mu.Unlock()
	if !ok {
		hash = a.dummy()
	}
	match := bcrypt.CompareHashAndPassword(hash, []byte(password)) == nil
	return ok && match
}

// CheckKey reports whether key is listed in authorized_keys. Any user name
// is accepted for a listed key.
func (a *Authenticator) CheckKey(user string, key ssh.PublicKey) bool {
	if key == nil {
		return false
	}
	a.mu.Lock()
	a.refreshLocked()
	keys := a.keys
	a.mu.Unlock()
	wire := key.Marshal()
	found := false
	for _, k := range keys {
		if len(k) == len(wire) && subtle.ConstantTimeCompare(k, wire) == 1 {
			found = true
		}
	}
	return found
}

// SetPassword creates or updates a user with a bcrypt hash of password.
func (a *Authenticator) SetPassword(user, password string) error {
	if err := ValidateName(user); err != nil {
		return err
	}
	if err := ValidatePassword(password); err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), cost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	return a.modifyUsers(func(users map[string][]byte) error {
		users[user] = hash
		return nil
	})
}

// RemoveUser deletes a user.
func (a *Authenticator) RemoveUser(user string) error {
	return a.modifyUsers(func(users map[string][]byte) error {
		if _, ok := users[user]; !ok {
			return fmt.Errorf("%w %q", ErrUnknownUser, user)
		}
		delete(users, user)
		return nil
	})
}

// modifyUsers re-reads users.json, applies fn and writes the result back
// atomically.
func (a *Authenticator) modifyUsers(fn func(map[string][]byte) error) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	users, err := readUsers(a.usersPath())
	if err != nil {
		return err
	}
	if err := fn(users); err != nil {
		return err
	}
	doc := usersDoc{Version: usersVersion, Users: make([]userRecord, 0, len(users))}
	for _, name := range sortedNames(users) {
		doc.Users = append(doc.Users, userRecord{Name: name, Hash: string(users[name])})
	}
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFileAtomic(a.usersPath(), append(raw, '\n')); err != nil {
		return err
	}
	st, err := stat(a.usersPath())
	if err != nil {
		return err
	}
	a.users, a.usersStamp, a.usersErr = users, st, nil
	return nil
}

// Users lists user names, sorted.
func (a *Authenticator) Users() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.refreshLocked()
	return sortedNames(a.users)
}

// HasCredentials reports whether at least one user or key exists.
func (a *Authenticator) HasCredentials() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.refreshLocked()
	return len(a.users) > 0 || len(a.keys) > 0
}

// Err returns the last error encountered while reading users.json or
// authorized_keys, or nil when both files are healthy. While a file is
// broken it is treated as empty.
func (a *Authenticator) Err() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.refreshLocked()
	return errors.Join(a.usersErr, a.keysErr)
}

func sortedNames(users map[string][]byte) []string {
	names := make([]string, 0, len(users))
	for name := range users {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// writeFileAtomic writes data to a temporary file in the same directory
// (os.CreateTemp creates it with 0600 permissions) and renames it over
// path.
func writeFileAtomic(path string, data []byte) (err error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(tmp)
		}
	}()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
