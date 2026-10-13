package state

import (
	"database/sql"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"

	"crypto/ed25519"
	"github.com/tailscale/tailcat"
	_ "modernc.org/sqlite"
	"tailscale.com/tailcfg"
)

// bootstrap.json is compiled into Mochi and is never written by the app.
// It is applied once during startup, before normal state reads begin.
//
//go:embed bootstrap.json
var bootstrapData []byte

type Bootstrap struct {
	Settings map[string]json.RawMessage `json:"settings"`
	// TrustedKeys are the Mochi machines whose approvals this build honors,
	// besides each host's own. Key is the machine's Ed25519 public key in hex.
	TrustedKeys []struct {
		Name string `json:"name"`
		Key  string `json:"key"`
	} `json:"trusted_keys"`
}

// TrustedKeys returns the keys in the embedded bootstrap. A malformed or
// duplicate entry is an error: a skipped key would silently shrink the trust set.
func TrustedKeys() ([]ed25519.PublicKey, error) {
	var bootstrap Bootstrap
	if err := json.Unmarshal(bootstrapData, &bootstrap); err != nil {
		return nil, fmt.Errorf("parse embedded bootstrap: %w", err)
	}
	seen := map[string]bool{}
	var out []ed25519.PublicKey
	for _, t := range bootstrap.TrustedKeys {
		raw, err := hex.DecodeString(t.Key)
		if err != nil || len(raw) != ed25519.PublicKeySize || t.Key != hex.EncodeToString(raw) {
			return nil, fmt.Errorf("bootstrap trusted key %q: want %d lowercase hex bytes", t.Name, ed25519.PublicKeySize)
		}
		if seen[t.Key] {
			return nil, fmt.Errorf("bootstrap trusted key %q is listed twice", t.Name)
		}
		seen[t.Key] = true
		out = append(out, ed25519.PublicKey(raw))
	}
	return out, nil
}

// OpenDB opens the state database the way every Mochi process must. The GUI
// writes the clients table as connections arrive, so a writer waits up to 5 s
// for another's write lock instead of failing at once with SQLITE_BUSY, and
// readers do not block the writer (WAL). Each process uses one connection.
func OpenDB(path string) (*sql.DB, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}
	if abs[0] != '/' { // a Windows drive path: file:///C:/Users/...
		u.Path = "/" + u.Path
	}
	u.RawQuery = url.Values{"_pragma": {"busy_timeout(5000)", "journal_mode(WAL)"}}.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

type StateStore struct {
	db   *sql.DB
	path string
}

func OpenState(path, legacyPath string) (*StateStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	db, err := OpenDB(path)
	if err != nil {
		return nil, err
	}
	store := &StateStore{db: db, path: path}
	if err := store.init(); err != nil {
		db.Close()
		return nil, err
	}
	if err := store.applyBootstrap(); err != nil {
		db.Close()
		return nil, err
	}
	if err := store.migrateLegacy(legacyPath); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

// Host is a Tailcat host this machine connects to.
type Host struct {
	Name    string
	Address string
}

func (s *StateStore) init() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS settings (
    key TEXT PRIMARY KEY NOT NULL,
    value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS hosts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    address TEXT NOT NULL UNIQUE
);
INSERT INTO settings(key, value) VALUES ('server_enabled', 'false')
    ON CONFLICT(key) DO NOTHING;
CREATE TABLE IF NOT EXISTS certs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    cert BLOB NOT NULL UNIQUE
);
CREATE TABLE IF NOT EXISTS received_texts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    sender TEXT NOT NULL,
    body TEXT NOT NULL,
    received_at INTEGER NOT NULL
);
`)
	return err
}

func (s *StateStore) applyBootstrap() error {
	var bootstrap Bootstrap
	if err := json.Unmarshal(bootstrapData, &bootstrap); err != nil {
		return fmt.Errorf("parse embedded bootstrap: %w", err)
	}
	for key, raw := range bootstrap.Settings {
		if _, err := s.db.Exec(`INSERT INTO settings(key, value) VALUES (?, ?)
ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, string(raw)); err != nil {
			return fmt.Errorf("apply bootstrap setting %q: %w", key, err)
		}
	}
	return nil
}

func (s *StateStore) migrateLegacy(path string) error {
	if path == "" {
		return nil
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM hosts`).Scan(&count); err != nil || count != 0 {
		return err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var legacy struct {
		Hosts []Host `json:"hosts"`
	}
	if err := json.Unmarshal(data, &legacy); err != nil {
		return fmt.Errorf("parse legacy host config: %w", err)
	}
	for _, host := range legacy.Hosts {
		if _, err := s.db.Exec(`INSERT INTO hosts(name, address) VALUES (?, ?)
ON CONFLICT(address) DO NOTHING`, host.Name, host.Address); err != nil {
			return err
		}
	}
	// The old JSON file is no longer application state after migration.
	return os.Remove(path)
}

func (s *StateStore) Hosts() ([]Host, error) {
	rows, err := s.db.Query(`SELECT name, address FROM hosts ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var hosts []Host
	for rows.Next() {
		var host Host
		if err := rows.Scan(&host.Name, &host.Address); err != nil {
			return nil, err
		}
		hosts = append(hosts, host)
	}
	return hosts, rows.Err()
}

func (s *StateStore) AddHost(host Host) error {
	_, err := s.db.Exec(`INSERT INTO hosts(name, address) VALUES (?, ?)`, host.Name, host.Address)
	return err
}

func (s *StateStore) DeleteHost(address string) error {
	_, err := s.db.Exec(`DELETE FROM hosts WHERE address = ?`, address)
	return err
}

func (s *StateStore) BoolSetting(key string) (bool, error) {
	var value string
	if err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&value); err != nil {
		return false, err
	}
	var result bool
	if err := json.Unmarshal([]byte(value), &result); err != nil {
		return false, fmt.Errorf("decode setting %q: %w", key, err)
	}
	return result, nil
}

func (s *StateStore) StringSetting(key string) (string, error) {
	var value string
	if err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&value); err != nil {
		return "", err
	}
	var result string
	if err := json.Unmarshal([]byte(value), &result); err != nil {
		return "", fmt.Errorf("decode setting %q: %w", key, err)
	}
	return result, nil
}

func (s *StateStore) SetBool(key string, value bool) error {
	encoded, _ := json.Marshal(value)
	_, err := s.db.Exec(`INSERT INTO settings(key, value) VALUES (?, ?)
ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, string(encoded))
	return err
}

// Config returns the launcher config saved under key. A missing key is an empty
// config, not an error, because a launcher that was never configured runs on its
// defaults.
func (s *StateStore) Config(key string) (map[string]string, error) {
	var value string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	var config map[string]string
	if err := json.Unmarshal([]byte(value), &config); err != nil {
		return nil, fmt.Errorf("decode config %q: %w", key, err)
	}
	if config == nil {
		config = map[string]string{}
	}
	return config, nil
}

// SetConfig saves the launcher config under key, replacing what was saved.
func (s *StateStore) SetConfig(key string, config map[string]string) error {
	encoded, err := json.Marshal(config)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO settings(key, value) VALUES (?, ?)
ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, string(encoded))
	return err
}

// Identity returns this machine's identity, creating it on first use. The seed
// is kept in the state database; a keychain is planned. Installs from before
// identities existed had a random client key that cannot become a seed, so
// they start a new identity.
func (s *StateStore) Identity() (Identity, error) {
	encoded, err := s.StringSetting("identity_seed")
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Identity{}, err
	}
	if err == nil && encoded != "" {
		seed, err := hex.DecodeString(encoded)
		if err != nil || len(seed) != ed25519.SeedSize {
			return Identity{}, errors.New("stored identity seed is malformed")
		}
		return IdentityFromSeed(seed), nil
	}
	id := NewIdentity()
	if err := s.SetString("identity_seed", hex.EncodeToString(id.Seed())); err != nil {
		return Identity{}, err
	}
	return id, nil
}

// ServerParams returns the pre-shared key and relay region this machine's
// server keeps across restarts, creating the key on first use. The region is
// zero until SaveRegion records one.
func (s *StateStore) ServerParams() (tailcat.PresharedKey, tailcfg.DERPRegionID, error) {
	var psk tailcat.PresharedKey
	encoded, err := s.StringSetting("server_psk")
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return psk, 0, err
	}
	if err == nil && encoded != "" {
		if err := psk.UnmarshalText([]byte(encoded)); err != nil {
			return psk, 0, fmt.Errorf("decode stored server key: %w", err)
		}
	} else {
		psk = tailcat.NewPresharedKey()
		text, err := psk.MarshalText()
		if err != nil {
			return psk, 0, err
		}
		if err := s.SetString("server_psk", string(text)); err != nil {
			return psk, 0, err
		}
	}
	region, err := s.StringSetting("server_region_id")
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return psk, 0, err
	}
	id, _ := strconv.Atoi(region)
	return psk, tailcfg.DERPRegionID(id), nil
}

func (s *StateStore) SaveRegion(id tailcfg.DERPRegionID) error {
	return s.SetString("server_region_id", strconv.Itoa(int(id)))
}

// Certs lists the certificates this machine presents to hosts.
func (s *StateStore) Certs() ([][]byte, error) {
	rows, err := s.db.Query(`SELECT cert FROM certs ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out [][]byte
	for rows.Next() {
		var c []byte
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *StateStore) AddCert(cert []byte) error {
	_, err := s.db.Exec(`INSERT INTO certs(cert) VALUES (?) ON CONFLICT(cert) DO NOTHING`, cert)
	return err
}

// DeleteSetting removes a setting. A missing key is not an error.
func (s *StateStore) DeleteSetting(key string) error {
	_, err := s.db.Exec(`DELETE FROM settings WHERE key = ?`, key)
	return err
}

func (s *StateStore) SetString(key, value string) error {
	encoded, _ := json.Marshal(value)
	_, err := s.db.Exec(`INSERT INTO settings(key, value) VALUES (?, ?)
ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, string(encoded))
	return err
}

func (s *StateStore) Close() error { return s.db.Close() }

// DB returns the database handle, for the admission gate, which keeps its own tables in it.
func (s *StateStore) DB() *sql.DB { return s.db }

// DatabasePath returns the file the store was opened from.
func (s *StateStore) DatabasePath() string { return s.path }
