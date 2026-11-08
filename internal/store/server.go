package store

import (
	"fmt"
	"net"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Limits applied by Server.Validate.
const (
	// DefaultPort is used when a server's Port is 0.
	DefaultPort = 22

	MaxNameLen    = 48
	MaxHostLen    = 253
	MaxKeyNameLen = 64
	MaxTags       = 8
	MaxTagLen     = 24

	// MaxKeyPathLen bounds legacy key paths still accepted when loading
	// servers.json (see Server.KeyPath).
	MaxKeyPathLen = 4096
)

// KeysDir is the directory inside the data directory that holds the
// per-server private keys named by Server.KeyPath.
const KeysDir = "keys"

var (
	userPattern    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]{0,31}$`)
	tagPattern     = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	keyNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
)

// Server is one managed host.
type Server struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Host string `json:"host"`
	Port int    `json:"port"`
	User string `json:"user"`
	// KeyPath optionally names a private key used for this server instead
	// of the manager's own key (<data>/id_ed25519, used when empty). It is
	// a plain file name (see ValidateKeyName), not a path: the key is read
	// from <data>/keys/<KeyPath>, which on Unix must be owned by the user
	// running vigil with no group or other permissions. Absolute or
	// relative paths stored by older versions still load, but are never
	// used for dialing and must be replaced by a key name on the next
	// edit.
	KeyPath   string    `json:"key_path,omitempty"`
	Tags      []string  `json:"tags,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Addr returns "host:port" suitable for net.Dial (IPv6 safe). A zero
// Port means DefaultPort.
func (s Server) Addr() string {
	port := s.Port
	if port == 0 {
		port = DefaultPort
	}
	return net.JoinHostPort(stripBrackets(strings.TrimSpace(s.Host)), strconv.Itoa(port))
}

// Normalized returns a cleaned-up copy of s as Add and Update store it:
// surrounding whitespace is trimmed, brackets around IPv6 literals are
// removed (and the address is canonicalized), a zero Port becomes
// DefaultPort and empty or duplicate tags (case-insensitive) are dropped.
func (s Server) Normalized() Server {
	s.Name = strings.TrimSpace(s.Name)
	s.Host = normalizeHost(s.Host)
	s.User = strings.TrimSpace(s.User)
	s.KeyPath = strings.TrimSpace(s.KeyPath)
	if s.Port == 0 {
		s.Port = DefaultPort
	}
	s.Tags = normalizeTags(s.Tags)
	return s
}

// Validate checks the fields a user can edit and returns a readable
// *FieldError. It validates the Normalized form of s, so a zero port or a
// bracketed IPv6 address is accepted. Name uniqueness is checked by Add and
// Update.
func (s Server) Validate() error {
	return s.validate(validateKeyName)
}

// validateStored is Validate for servers loaded from servers.json: legacy
// key paths are accepted so the file keeps loading (the dialer refuses
// them).
func (s Server) validateStored() error {
	return s.validate(validateLegacyKeyPath)
}

func (s Server) validate(validateKey func(string) error) error {
	n := s.Normalized()
	checks := []func() error{
		func() error { return validateName(n.Name) },
		func() error { return validateHost(n.Host) },
		func() error { return validatePort(n.Port) },
		func() error { return validateUser(n.User) },
		func() error { return validateKey(n.KeyPath) },
		func() error { return validateTags(n.Tags) },
	}
	for _, check := range checks {
		if err := check(); err != nil {
			return err
		}
	}
	return nil
}

func (s Server) clone() Server {
	if s.Tags != nil {
		s.Tags = append([]string(nil), s.Tags...)
	}
	return s
}

func validateName(name string) error {
	switch {
	case name == "":
		return fieldErr("name", "must not be empty")
	case !utf8.ValidString(name):
		return fieldErr("name", "must be valid UTF-8")
	case utf8.RuneCountInString(name) > MaxNameLen:
		return fieldErr("name", fmt.Sprintf("must be at most %d characters", MaxNameLen))
	case hasControl(name):
		return fieldErr("name", "must not contain control characters")
	}
	return nil
}

func validateHost(host string) error {
	if host == "" {
		return fieldErr("host", "must not be empty")
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return nil
	}
	if !isHostname(host) {
		return fieldErr("host", fmt.Sprintf("%q is not a valid hostname or IP", host))
	}
	return nil
}

func validatePort(port int) error {
	if port < 1 || port > 65535 {
		return fieldErr("port", fmt.Sprintf("%d is out of range (1–65535)", port))
	}
	return nil
}

func validateUser(user string) error {
	if user == "" {
		return fieldErr("user", "must not be empty")
	}
	if !userPattern.MatchString(user) {
		return fieldErr("user", fmt.Sprintf("%q is not a valid user name (letters, digits, '_', '.', '-'; at most 32 characters; must not start with a digit)", user))
	}
	return nil
}

// ValidateKeyName checks a per-server key name: a plain file name inside
// <data>/keys of 1–MaxKeyNameLen characters from A–Z a–z 0–9 . _ -,
// starting with a letter or digit. Paths ("/", "\", ".."), "~" and hidden
// names are rejected. The error is a *FieldError for "key_path".
func ValidateKeyName(name string) error {
	switch {
	case name == "":
		return fieldErr("key_path", "must not be empty")
	case len(name) > MaxKeyNameLen:
		return fieldErr("key_path", fmt.Sprintf("must be at most %d characters: give the file name of a key in the vigil keys directory, not a path", MaxKeyNameLen))
	case !keyNamePattern.MatchString(name):
		return fieldErr("key_path", fmt.Sprintf("%q is not a valid key name: give the file name of a key in the vigil keys directory (A–Z, a–z, 0–9, '.', '_' or '-', starting with a letter or digit), not a path", name))
	}
	return nil
}

func validateKeyName(name string) error {
	if name == "" {
		return nil
	}
	return ValidateKeyName(name)
}

func validateLegacyKeyPath(path string) error {
	switch {
	case path == "":
		return nil
	case len(path) > MaxKeyPathLen:
		return fieldErr("key_path", fmt.Sprintf("must be at most %d bytes", MaxKeyPathLen))
	case !utf8.ValidString(path):
		return fieldErr("key_path", "must be valid UTF-8")
	case hasControl(path):
		return fieldErr("key_path", "must not contain control characters")
	}
	return nil
}

func validateTags(tags []string) error {
	if len(tags) > MaxTags {
		return fieldErr("tags", fmt.Sprintf("at most %d tags are allowed (got %d)", MaxTags, len(tags)))
	}
	for _, tag := range tags {
		if len(tag) > MaxTagLen || !tagPattern.MatchString(tag) {
			return fieldErr("tags", fmt.Sprintf("%q must be 1–%d characters of A–Z, a–z, 0–9, '.', '_' or '-'", tag, MaxTagLen))
		}
	}
	return nil
}

// isHostname reports whether host is a syntactically valid DNS name
// (RFC 1123 labels, optional trailing dot, last label not all-numeric).
func isHostname(host string) bool {
	host = strings.TrimSuffix(host, ".")
	if host == "" || len(host) > MaxHostLen {
		return false
	}
	labels := strings.Split(host, ".")
	for _, label := range labels {
		if !isLabel(label) {
			return false
		}
	}
	return !isDigits(labels[len(labels)-1])
}

func isLabel(label string) bool {
	if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
		return false
	}
	for i := range len(label) {
		c := label[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}

func isDigits(s string) bool {
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return s != ""
}

func hasControl(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool {
		return unicode.IsControl(r) || unicode.Is(unicode.Cf, r)
	})
}

func normalizeHost(host string) string {
	host = stripBrackets(strings.TrimSpace(host))
	if addr, err := netip.ParseAddr(host); err == nil {
		return addr.String()
	}
	return host
}

func stripBrackets(host string) string {
	if len(host) >= 2 && host[0] == '[' && host[len(host)-1] == ']' {
		return host[1 : len(host)-1]
	}
	return host
}

func normalizeTags(tags []string) []string {
	var out []string
	seen := make(map[string]bool, len(tags))
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		key := strings.ToLower(tag)
		if tag == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, tag)
	}
	return out
}
