package state

import (
	"crypto/ed25519"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/tailscale/tailcat"
	"tailscale.com/tailcfg"
)

// bootstrap.json is compiled into Mochi and is never written by the app. It is
// written by hand, so it stays JSON; nothing Mochi stores or sends is.
//
//go:embed bootstrap.json
var bootstrapData []byte

type Bootstrap struct {
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

// Host is a Tailcat host this machine connects to.
type Host struct {
	Name    string
	Address string
}

// Store is this machine's state: an embedded PocketBase app with the
// collections of schema.go. PocketBase is never served; Mochi reads and
// writes it through the record API only.
type Store struct{ app *core.BaseApp }

// machineID is the id of the one record of the machine collection.
const machineID = "machine00000000"

// Open bootstraps PocketBase in dataDir, creates any missing collections and
// creates the machine record, with a new identity and server key, on first use.
func Open(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, err
	}
	app := core.NewBaseApp(core.BaseAppConfig{DataDir: dataDir})
	if err := app.Bootstrap(); err != nil {
		return nil, err
	}
	s := &Store{app: app}
	if err := s.init(); err != nil {
		_ = app.ClearBootstrap()
		return nil, err
	}
	return s, nil
}

func (s *Store) init() error {
	if err := s.app.RunSystemMigrations(); err != nil {
		return err
	}
	if err := s.ensureSchema(); err != nil {
		return err
	}
	// Mochi logs to mochi.log; PocketBase's own log table in auxiliary.db
	// stays empty.
	if settings := s.app.Settings(); settings.Logs.MaxDays != 0 {
		settings.Logs.MaxDays = 0
		if err := s.app.Save(settings); err != nil {
			return err
		}
	}
	if _, err := s.machine(); !missing(err) {
		return err
	}
	psk, err := tailcat.NewPresharedKey().MarshalText()
	if err != nil {
		return err
	}
	r := core.NewRecord(s.collection("machine"))
	r.Id = machineID
	r.Set("server_enabled", false)
	r.Set("server_psk", string(psk))
	r.Set("server_region", 0)
	r.Set("identity_seed", hex.EncodeToString(NewIdentity().Seed()))
	return s.app.Save(r)
}

// App is the PocketBase app, for the admission gate, which keeps the clients
// collection.
func (s *Store) App() core.App { return s.app }

// DataDir is the directory PocketBase keeps its databases in.
func (s *Store) DataDir() string { return s.app.DataDir() }

// Close shuts PocketBase down the way its own command does: the terminate
// hooks stop its directory watcher and remove this instance's notify files,
// then the databases close.
func (s *Store) Close() error {
	return s.app.OnTerminate().Trigger(&core.TerminateEvent{App: s.app}, func(e *core.TerminateEvent) error {
		return e.App.ClearBootstrap()
	})
}

// missing reports whether a lookup found no record.
func missing(err error) bool { return errors.Is(err, sql.ErrNoRows) }

// collection returns a collection that ensureSchema created, so it exists.
func (s *Store) collection(name string) *core.Collection {
	c, err := s.app.FindCachedCollectionByNameOrId(name)
	if err != nil {
		panic(fmt.Sprintf("collection %s: %v", name, err))
	}
	return c
}

func (s *Store) machine() (*core.Record, error) {
	return s.app.FindRecordById("machine", machineID)
}

// Hosts returns the hosts in the order they were added.
func (s *Store) Hosts() ([]Host, error) {
	recs, err := s.app.FindRecordsByFilter("hosts", "", "@rowid", 0, 0)
	if err != nil {
		return nil, err
	}
	out := make([]Host, 0, len(recs))
	for _, r := range recs {
		out = append(out, Host{Name: r.GetString("name"), Address: r.GetString("address")})
	}
	return out, nil
}

// AddHost saves a host. An address that is already saved is an error.
func (s *Store) AddHost(h Host) error {
	r := core.NewRecord(s.collection("hosts"))
	r.Set("name", h.Name)
	r.Set("address", h.Address)
	return s.app.Save(r)
}

// DeleteHost removes the host at address, and with it, through the relation's
// cascade, every launcher setting kept for it. A missing host is not an error.
func (s *Store) DeleteHost(address string) error {
	r, err := s.app.FindFirstRecordByData("hosts", "address", address)
	if missing(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return s.app.Delete(r)
}

// ServerEnabled reports whether the Tailcat server should run.
func (s *Store) ServerEnabled() (bool, error) {
	r, err := s.machine()
	if err != nil {
		return false, err
	}
	return r.GetBool("server_enabled"), nil
}

// SetServerEnabled saves whether the Tailcat server should run.
func (s *Store) SetServerEnabled(on bool) error {
	r, err := s.machine()
	if err != nil {
		return err
	}
	r.Set("server_enabled", on)
	return s.app.Save(r)
}

// Serving reports whether a launcher's server side was left on. A launcher
// that was never switched is off.
func (s *Store) Serving(launcher string) (bool, error) {
	r, err := s.app.FindFirstRecordByData("launchers", "name", strings.ToLower(launcher))
	if missing(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return r.GetBool("serving"), nil
}

// SetServing saves a launcher's server-side switch.
func (s *Store) SetServing(launcher string, on bool) error {
	name := strings.ToLower(launcher)
	r, err := s.app.FindFirstRecordByData("launchers", "name", name)
	if missing(err) {
		r = core.NewRecord(s.collection("launchers"))
		r.Set("name", name)
	} else if err != nil {
		return err
	}
	r.Set("serving", on)
	return s.app.Save(r)
}

// settingsFilter selects one launcher's settings for one host, or for the
// server side when host is "".
const settingsFilter = "launcher = {:launcher} && host = {:host}"

// hostID returns the record id of the host at address; ok is false if there is
// no such host.
func (s *Store) hostID(address string) (id string, ok bool, err error) {
	r, err := s.app.FindFirstRecordByData("hosts", "address", address)
	if missing(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return r.Id, true, nil
}

// LauncherConfig returns the settings saved for a launcher: its server side
// when hostAddress is "", or its client side for that host. Nothing saved is an
// empty config, not an error, because a launcher that was never configured runs
// on its defaults.
func (s *Store) LauncherConfig(launcher, hostAddress string) (map[string]string, error) {
	host := ""
	if hostAddress != "" {
		id, ok, err := s.hostID(hostAddress)
		if err != nil || !ok {
			return map[string]string{}, err
		}
		host = id
	}
	recs, err := s.app.FindRecordsByFilter("launcher_settings", settingsFilter, "", 0, 0,
		dbx.Params{"launcher": strings.ToLower(launcher), "host": host})
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(recs))
	for _, r := range recs {
		out[r.GetString("key")] = r.GetString("value")
	}
	return out, nil
}

// SetLauncherConfig replaces the settings saved for a launcher's server side
// (hostAddress "") or for its client side on that host, in one transaction.
func (s *Store) SetLauncherConfig(launcher, hostAddress string, cfg map[string]string) error {
	host := ""
	if hostAddress != "" {
		id, ok, err := s.hostID(hostAddress)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("no host at %s", hostAddress)
		}
		host = id
	}
	launcher = strings.ToLower(launcher)
	return s.app.RunInTransaction(func(tx core.App) error {
		old, err := tx.FindRecordsByFilter("launcher_settings", settingsFilter, "", 0, 0,
			dbx.Params{"launcher": launcher, "host": host})
		if err != nil {
			return err
		}
		for _, r := range old {
			if err := tx.Delete(r); err != nil {
				return err
			}
		}
		for k, v := range cfg {
			r := core.NewRecord(s.collection("launcher_settings"))
			r.Set("launcher", launcher)
			r.Set("host", host)
			r.Set("key", k)
			r.Set("value", v)
			if err := tx.Save(r); err != nil {
				return err
			}
		}
		return nil
	})
}

// Identity returns this machine's identity. The seed is kept in the machine
// record; #12 moves it to the OS credential store.
func (s *Store) Identity() (Identity, error) {
	r, err := s.machine()
	if err != nil {
		return Identity{}, err
	}
	seed, err := hex.DecodeString(r.GetString("identity_seed"))
	if err != nil || len(seed) != ed25519.SeedSize {
		return Identity{}, errors.New("stored identity seed is malformed")
	}
	return IdentityFromSeed(seed), nil
}

// ServerParams returns the pre-shared key and relay region this machine's
// server keeps across restarts. The region is zero until SaveRegion records one.
func (s *Store) ServerParams() (tailcat.PresharedKey, tailcfg.DERPRegionID, error) {
	var psk tailcat.PresharedKey
	r, err := s.machine()
	if err != nil {
		return psk, 0, err
	}
	if err := psk.UnmarshalText([]byte(r.GetString("server_psk"))); err != nil {
		return psk, 0, fmt.Errorf("decode stored server key: %w", err)
	}
	return psk, tailcfg.DERPRegionID(r.GetInt("server_region")), nil
}

// SaveRegion records the relay region the server settled on.
func (s *Store) SaveRegion(id tailcfg.DERPRegionID) error {
	r, err := s.machine()
	if err != nil {
		return err
	}
	r.Set("server_region", int(id))
	return s.app.Save(r)
}

// Certs lists the certificates this machine presents to hosts, newest first.
// They are CBOR, kept hex-encoded because PocketBase has no bytes field.
func (s *Store) Certs() ([][]byte, error) {
	recs, err := s.app.FindRecordsByFilter("certs", "", "-@rowid", 0, 0)
	if err != nil {
		return nil, err
	}
	out := make([][]byte, 0, len(recs))
	for _, r := range recs {
		b, err := hex.DecodeString(r.GetString("cert"))
		if err != nil {
			return nil, fmt.Errorf("stored certificate %s: %w", r.Id, err)
		}
		out = append(out, b)
	}
	return out, nil
}

// AddCert saves a certificate. One already saved is not saved twice.
func (s *Store) AddCert(cert []byte) error {
	v := hex.EncodeToString(cert)
	_, err := s.app.FindFirstRecordByData("certs", "cert", v)
	if !missing(err) {
		return err
	}
	r := core.NewRecord(s.collection("certs"))
	r.Set("cert", v)
	return s.app.Save(r)
}

// DeleteCerts forgets every saved certificate.
func (s *Store) DeleteCerts() error {
	recs, err := s.app.FindAllRecords("certs")
	if err != nil {
		return err
	}
	for _, r := range recs {
		if err := s.app.Delete(r); err != nil {
			return err
		}
	}
	return nil
}
