// Package store persists the managed server inventory and manager settings
// as JSON files in the data directory.
//
// Files are written atomically (temporary file, fsync, rename) with mode
// 0600 inside a versioned envelope:
//
//	{"version": 1, "servers": [...]}
//	{"version": 1, "settings": {...}}
package store

import (
	"cmp"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// Store is safe for concurrent use by many SSH sessions.
type Store struct {
	mu       sync.RWMutex
	servers  []Server
	settings Settings
	version  uint64
	// dir is the data directory; empty means in-memory only.
	dir string
	now func() time.Time
}

// Open loads (or initializes) servers.json and settings.json in dir,
// creating dir with mode 0700 if needed. Missing files mean an empty
// inventory and DefaultSettings; settings fields missing from the file
// take their default. A corrupt or invalid file is reported as an error and
// left untouched.
func Open(dir string) (*Store, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("store: data directory must not be empty")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("store: create data directory: %w", err)
	}
	servers, err := loadServers(filepath.Join(dir, serversFile))
	if err != nil {
		return nil, err
	}
	settings, err := loadSettings(filepath.Join(dir, settingsFile))
	if err != nil {
		return nil, err
	}
	return &Store{dir: dir, servers: servers, settings: settings, now: time.Now}, nil
}

// NewMemory returns an empty store with DefaultSettings that is never
// written to disk (used by `vigil demo`, `vigil render` and tests).
func NewMemory() *Store {
	return &Store{settings: DefaultSettings(), now: time.Now}
}

// Dir returns the data directory, or "" for an in-memory store.
func (s *Store) Dir() string { return s.dir }

// Version increases on every successful mutation; callers can compare
// it to detect changes cheaply.
func (s *Store) Version() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.version
}

// Servers returns a copy of all servers sorted by name
// (case-insensitive).
func (s *Store) Servers() []Server {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Server, len(s.servers))
	for i, srv := range s.servers {
		out[i] = srv.clone()
	}
	return out
}

// Server returns one server by ID.
func (s *Store) Server(id string) (Server, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if i := s.indexOf(id); i >= 0 {
		return s.servers[i].clone(), true
	}
	return Server{}, false
}

// Add normalizes and validates srv, assigns a new ID and timestamps,
// persists and returns the stored server. Any ID or timestamps set by the
// caller are ignored.
func (s *Store) Add(srv Server) (Server, error) {
	srv = srv.Normalized()
	if err := srv.Validate(); err != nil {
		return Server{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkUniqueName(srv.Name, ""); err != nil {
		return Server{}, err
	}
	srv.ID = s.newID()
	srv.CreatedAt = s.clock()
	srv.UpdatedAt = srv.CreatedAt

	next := append(slices.Clone(s.servers), srv.clone())
	sortServers(next)
	if err := s.commitServers(next); err != nil {
		return Server{}, err
	}
	return srv.clone(), nil
}

// Update replaces the editable fields of the server with the same ID. It
// keeps CreatedAt and sets UpdatedAt. It returns ErrNotFound for unknown
// IDs.
func (s *Store) Update(srv Server) error {
	srv = srv.Normalized()
	if err := srv.Validate(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.indexOf(srv.ID)
	if i < 0 {
		return ErrNotFound
	}
	if err := s.checkUniqueName(srv.Name, srv.ID); err != nil {
		return err
	}
	srv.CreatedAt = s.servers[i].CreatedAt
	srv.UpdatedAt = s.clock()

	next := slices.Clone(s.servers)
	next[i] = srv.clone()
	sortServers(next)
	return s.commitServers(next)
}

// Delete removes a server by ID. It returns ErrNotFound for unknown IDs.
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.indexOf(id)
	if i < 0 {
		return ErrNotFound
	}
	return s.commitServers(slices.Delete(slices.Clone(s.servers), i, i+1))
}

// Settings returns the current settings.
func (s *Store) Settings() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.settings
}

// SaveSettings validates, persists and applies settings.
func (s *Store) SaveSettings(set Settings) error {
	if err := set.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dir != "" {
		env := settingsEnvelope{Version: fileVersion, Settings: toSettingsJSON(set)}
		if err := writeJSONAtomic(filepath.Join(s.dir, settingsFile), env); err != nil {
			return err
		}
	}
	s.settings = set
	s.version++
	return nil
}

// commitServers persists next and, only if that succeeds, makes it the
// current inventory. The caller holds s.mu for writing.
func (s *Store) commitServers(next []Server) error {
	if s.dir != "" {
		env := serversEnvelope{Version: fileVersion, Servers: next}
		if env.Servers == nil {
			env.Servers = []Server{}
		}
		if err := writeJSONAtomic(filepath.Join(s.dir, serversFile), env); err != nil {
			return err
		}
	}
	s.servers = next
	s.version++
	return nil
}

func (s *Store) indexOf(id string) int {
	if id == "" {
		return -1
	}
	return slices.IndexFunc(s.servers, func(srv Server) bool { return srv.ID == id })
}

func (s *Store) checkUniqueName(name, exceptID string) error {
	for _, srv := range s.servers {
		if srv.ID != exceptID && strings.EqualFold(srv.Name, name) {
			return &FieldError{Field: "name", Msg: fmt.Sprintf("%q is already used by another server", name), Err: ErrDuplicateName}
		}
	}
	return nil
}

// newID returns a random 16-character hex ID not used by any server. The
// caller holds s.mu.
func (s *Store) newID() string {
	for {
		var b [8]byte
		_, _ = rand.Read(b[:])
		id := hex.EncodeToString(b[:])
		if s.indexOf(id) < 0 {
			return id
		}
	}
}

func (s *Store) clock() time.Time {
	now := time.Now
	if s.now != nil {
		now = s.now
	}
	return now().UTC().Round(0)
}

func sortServers(servers []Server) {
	slices.SortStableFunc(servers, func(a, b Server) int {
		return cmp.Or(
			cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)),
			cmp.Compare(a.ID, b.ID),
		)
	})
}
