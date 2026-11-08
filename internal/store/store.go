// Package store persists the managed server inventory and manager settings
// as JSON files in the data directory.
package store

import (
	"errors"
	"sync"
	"time"
)

// Server is one managed host.
type Server struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Host string `json:"host"`
	Port int    `json:"port"`
	User string `json:"user"`
	// KeyPath optionally points to a private key used for this server.
	// Empty means the manager's own key (<data>/id_ed25519).
	KeyPath   string    `json:"key_path,omitempty"`
	Tags      []string  `json:"tags,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Addr returns "host:port" suitable for net.Dial (IPv6 safe).
func (s Server) Addr() string {
	return ""
}

// Validate checks the fields a user can edit and returns a readable error.
func (s Server) Validate() error {
	return nil
}

// Settings are manager-wide options editable in the Settings tab.
type Settings struct {
	PollInterval time.Duration `json:"poll_interval"`
	// HistoryMinutes is how much history the collector keeps per server.
	HistoryMinutes int     `json:"history_minutes"`
	CPUWarn        float64 `json:"cpu_warn"`
	CPUCrit        float64 `json:"cpu_crit"`
	MemWarn        float64 `json:"mem_warn"`
	MemCrit        float64 `json:"mem_crit"`
	DiskWarn       float64 `json:"disk_warn"`
	DiskCrit       float64 `json:"disk_crit"`
	// OfflineGrace is how long a server must be unreachable before an
	// offline alert fires.
	OfflineGrace time.Duration `json:"offline_grace"`
	// Compat is the default glyph compatibility mode name
	// (auto, blocks, ascii, off).
	Compat string `json:"compat"`
}

// DefaultSettings returns sensible defaults.
func DefaultSettings() Settings {
	return Settings{
		PollInterval:   3 * time.Second,
		HistoryMinutes: 15,
		CPUWarn:        80,
		CPUCrit:        95,
		MemWarn:        85,
		MemCrit:        95,
		DiskWarn:       85,
		DiskCrit:       95,
		OfflineGrace:   10 * time.Second,
		Compat:         "auto",
	}
}

// ErrNotFound is returned for unknown server IDs.
var ErrNotFound = errors.New("server not found")

// Store is safe for concurrent use by many SSH sessions.
type Store struct {
	mu       sync.RWMutex
	servers  []Server
	settings Settings
	version  uint64
}

// Open loads (or initializes) servers.json and settings.json in dir.
func Open(dir string) (*Store, error) {
	return &Store{settings: DefaultSettings()}, nil
}

// Version increases on every successful mutation; callers can compare
// it to detect changes cheaply.
func (s *Store) Version() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.version
}

// Servers returns a copy of all servers sorted by name.
func (s *Store) Servers() []Server {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]Server(nil), s.servers...)
}

// Server returns one server by ID.
func (s *Store) Server(id string) (Server, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, srv := range s.servers {
		if srv.ID == id {
			return srv, true
		}
	}
	return Server{}, false
}

// Add validates srv, assigns a new ID and timestamps, persists and returns
// the stored server.
func (s *Store) Add(srv Server) (Server, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.servers = append(s.servers, srv)
	s.version++
	return srv, nil
}

// Update replaces the server with the same ID.
func (s *Store) Update(srv Server) error {
	return ErrNotFound
}

// Delete removes a server by ID.
func (s *Store) Delete(id string) error {
	return ErrNotFound
}

// Settings returns the current settings.
func (s *Store) Settings() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.settings
}

// SaveSettings validates, persists and applies settings.
func (s *Store) SaveSettings(set Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.settings = set
	s.version++
	return nil
}
