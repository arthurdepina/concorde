// Package accounts holds who may open a room: the admin, and the friends the
// admin lets broadcast. Only the admin gets the admin panel; a broadcaster
// account can do exactly one extra thing over a plain visitor — create a room
// and be its broadcaster.
//
// Passwords are never stored, only bcrypt hashes. That is not what stops
// someone guessing over the internet (the login lockout does that); it is what
// keeps a leaked file — a backup, an SD card, a stray commit — from handing over
// passwords that friends very likely reuse somewhere else.
//
// The admin is deliberately not in this file: it comes from the environment as
// ADMIN_PASSWORD_HASH and is installed in memory with SetAdmin. That way losing
// or corrupting the accounts file can never lock the admin out of the panel that
// manages it, and an account file that somehow says "admin" cannot mint one.
package accounts

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// Roles an account can have. Only broadcaster is ever stored in the file.
const (
	RoleAdmin       = "admin"
	RoleBroadcaster = "broadcaster"
)

// HashCost is the bcrypt cost for new passwords. It is a variable so tests can
// drop it to bcrypt.MinCost; at the default, one verification takes a couple of
// hundred milliseconds by design.
var HashCost = 12

// Password rules. The maximum is not ours: bcrypt silently ignores everything
// past 72 bytes, and a password that is quietly truncated is worse than one
// that is refused.
const (
	MinPasswordLength = 10
	MaxPasswordBytes  = 72
)

// dummyHash is compared against when the named account does not exist, so a
// wrong name and a wrong password take the same time to refuse. Without it, the
// response time would say which names are real. (bcrypt of a random string.)
const dummyHash = "$2a$12$/7eDRA2KK2U5pBpI4v7du.UUIps.ncqQcD5Pn2SMZE8nmJunq.Sye"

// verifyTTL is how long a correct credential stays cached. The admin panel
// re-authenticates on every poll (twice every few seconds), and bcrypt is
// expensive on purpose — without this cache an open panel would keep a core
// busy, which matters on the small machine this is headed for.
const verifyTTL = 5 * time.Minute

var (
	ErrNotFound      = errors.New("no such account")
	ErrExists        = errors.New("account already exists")
	ErrBuiltin       = errors.New("the admin account is configured in the environment, not here")
	ErrBadName       = errors.New("name must be 1-32 characters of a-z, 0-9, dot, dash or underscore")
	ErrShortPassword = fmt.Errorf("password must be at least %d characters", MinPasswordLength)
	ErrLongPassword  = fmt.Errorf("password must be at most %d bytes", MaxPasswordBytes)
)

var nameRE = regexp.MustCompile(`^[a-z0-9._-]{1,32}$`)

// Account is one login. Hash never leaves the package in anything served to a
// client — see Hub's admin handlers, which build their own view.
type Account struct {
	Name      string    `json:"name"`
	Role      string    `json:"role"`
	Hash      string    `json:"hash"`
	CreatedAt time.Time `json:"createdAt"`

	// Builtin marks the admin installed from the environment: it is not in the
	// file, is not written to it, and cannot be edited or removed through the
	// panel. Kept out of the JSON so it can never be set by editing the file.
	Builtin bool `json:"-"`
}

// Store is the set of accounts, backed by a JSON file. A store with an empty
// path keeps everything in memory (tests, and the case where only the
// environment admin exists).
type Store struct {
	path string

	mu     sync.RWMutex
	byName map[string]Account

	// verified caches credentials that already passed bcrypt: fingerprint ->
	// expiry. Any change to the accounts empties it, so a removed account or a
	// changed password takes effect at once.
	verified  map[string]time.Time
	lastPrune time.Time
}

// Open loads the accounts file, or returns an empty store if it does not exist
// yet. A file that exists but cannot be read or parsed is an error: starting up
// as if nobody had an account would quietly lock everyone out.
func Open(path string) (*Store, error) {
	s := &Store{
		path:     path,
		byName:   make(map[string]Account),
		verified: make(map[string]time.Time),
	}
	if path == "" {
		return s, nil
	}

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	var stored []Account
	if err := json.Unmarshal(data, &stored); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	for _, acc := range stored {
		name := normalizeName(acc.Name)
		if !nameRE.MatchString(name) {
			return nil, fmt.Errorf("%s: invalid account name %q", path, acc.Name)
		}
		// The admin comes from the environment. A file claiming otherwise is
		// either hand-edited or tampered with; either way, refuse it loudly.
		if acc.Role != RoleBroadcaster {
			return nil, fmt.Errorf("%s: account %q has role %q, only %q is allowed here",
				path, acc.Name, acc.Role, RoleBroadcaster)
		}
		if acc.Hash == "" {
			return nil, fmt.Errorf("%s: account %q has no password hash", path, acc.Name)
		}
		acc.Name = name
		s.byName[name] = acc
	}
	return s, nil
}

// SetAdmin installs the environment-configured admin. Passing an empty hash
// removes it. The account lives only in memory and is never written to the file.
func (s *Store) SetAdmin(name, hash string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for key, acc := range s.byName {
		if acc.Builtin {
			delete(s.byName, key)
		}
	}
	clear(s.verified)
	if hash == "" {
		return
	}
	key := normalizeName(name)
	s.byName[key] = Account{Name: key, Role: RoleAdmin, Hash: hash, Builtin: true}
}

// Verify checks a name and password, answering with the account on success. It
// takes the same time whether the name exists or not, so it does not reveal who
// has an account.
func (s *Store) Verify(name, password string) (Account, bool) {
	key := normalizeName(name)
	now := time.Now()

	s.mu.RLock()
	acc, found := s.byName[key]
	cached := found && s.verified[fingerprint(key, password)].After(now)
	s.mu.RUnlock()

	if cached {
		return acc, true
	}

	hash := dummyHash
	if found {
		hash = acc.Hash
	}
	// Runs outside the lock: bcrypt is slow by design and must not block anyone.
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil || !found {
		return Account{}, false
	}

	s.mu.Lock()
	s.pruneLocked(now)
	s.verified[fingerprint(key, password)] = now.Add(verifyTTL)
	s.mu.Unlock()
	return acc, true
}

// Add creates a broadcaster account and saves the file.
func (s *Store) Add(name, password string) error {
	key := normalizeName(name)
	if !nameRE.MatchString(key) {
		return ErrBadName
	}
	hash, err := Hash(password)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.byName[key]; exists {
		return ErrExists
	}
	s.byName[key] = Account{Name: key, Role: RoleBroadcaster, Hash: hash, CreatedAt: time.Now()}
	clear(s.verified)
	return s.saveLocked()
}

// SetPassword replaces an account's password.
func (s *Store) SetPassword(name, password string) error {
	hash, err := Hash(password)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	acc, ok := s.byName[normalizeName(name)]
	if !ok {
		return ErrNotFound
	}
	if acc.Builtin {
		return ErrBuiltin
	}
	acc.Hash = hash
	s.byName[acc.Name] = acc
	clear(s.verified)
	return s.saveLocked()
}

// Remove deletes an account.
func (s *Store) Remove(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	acc, ok := s.byName[normalizeName(name)]
	if !ok {
		return ErrNotFound
	}
	if acc.Builtin {
		return ErrBuiltin
	}
	delete(s.byName, acc.Name)
	clear(s.verified)
	return s.saveLocked()
}

// List returns every account, admin first then alphabetically. The hashes come
// with it — callers serving this to a client must build their own view.
func (s *Store) List() []Account {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]Account, 0, len(s.byName))
	for _, acc := range s.byName {
		out = append(out, acc)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Builtin != out[j].Builtin {
			return out[i].Builtin
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// GeneratePassword returns a strong password to hand to a friend (or to run
// with for one boot when no admin is configured) — better than one someone
// makes up on the spot. The alphabet leaves out characters that are easy to
// misread when a password is copied by hand.
func GeneratePassword() string {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789"
	const length = 16 // ~92 bits from this alphabet
	b := make([]byte, length)
	if _, err := rand.Read(b); err != nil {
		panic("accounts: out of entropy: " + err.Error())
	}
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}

// Hash turns a password into a bcrypt hash, refusing the ones that would be
// weak or silently truncated.
func Hash(password string) (string, error) {
	if len([]rune(password)) < MinPasswordLength {
		return "", ErrShortPassword
	}
	if len(password) > MaxPasswordBytes {
		return "", ErrLongPassword
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), HashCost)
	return string(hash), err
}

// saveLocked writes the persistable accounts out. It writes a temporary file
// next to the target and renames it over, so an interrupted write can never
// leave a half-written account list behind. Caller must hold s.mu.
func (s *Store) saveLocked() error {
	if s.path == "" {
		return nil
	}

	stored := make([]Account, 0, len(s.byName))
	for _, acc := range s.byName {
		if !acc.Builtin {
			stored = append(stored, acc)
		}
	}
	sort.Slice(stored, func(i, j int) bool { return stored[i].Name < stored[j].Name })

	data, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	tmp := s.path + ".tmp"
	// 0600: only the account running the server. Advisory on Windows, real on
	// the Linux host this is headed for.
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("replace %s: %w", s.path, err)
	}
	return nil
}

// pruneLocked drops expired cache entries, at most once a minute, so the map
// cannot grow without bound. Caller must hold s.mu.
func (s *Store) pruneLocked(now time.Time) {
	if now.Sub(s.lastPrune) < time.Minute {
		return
	}
	s.lastPrune = now
	for key, expiry := range s.verified {
		if !expiry.After(now) {
			delete(s.verified, key)
		}
	}
}

// fingerprint keys the verification cache without keeping the password around
// in a map for its whole TTL.
func fingerprint(name, password string) string {
	sum := sha256.Sum256([]byte(name + "\x00" + password))
	return hex.EncodeToString(sum[:])
}

// normalizeName makes lookups forgiving: names are case-insensitive and never
// carry stray spaces, so "Ana " and "ana" are the same friend, not two.
func normalizeName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// Path is where this store persists, for logging and diagnostics.
func (s *Store) Path() string {
	if s.path == "" {
		return "(memory only)"
	}
	abs, err := filepath.Abs(s.path)
	if err != nil {
		return s.path
	}
	return abs
}
