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

//go:embed bootstrap.json
var bootstrapData []byte

type Bootstrap struct {
	TrustedKeys []struct {
		Name string `json:"name"`
		Key  string `json:"key"`
	} `json:"trusted_keys"`
}

// TrustedKeys returns the build-time keys trusted to approve clients.
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

type Host struct {
	Name    string
	Address string
}
type Store struct{ app *core.BaseApp }

func (s *Store) App() core.App   { return s.app }
func (s *Store) DataDir() string { return s.app.DataDir() }
func (s *Store) Close() error    { return s.app.ClearBootstrap() }
func (s *Store) machine() (*core.Record, error) {
	return s.app.FindRecordById("machine", "machine00000000")
}
func mustCollection(app core.App, name string) *core.Collection {
	c, _ := app.FindCollectionByNameOrId(name)
	return c
}

// recordMissing reports whether a PocketBase lookup found no record.
func recordMissing(err error) bool { return errors.Is(err, sql.ErrNoRows) }
func lower(s string) string        { return strings.ToLower(s) }

// Open bootstraps the embedded PocketBase application and creates the machine record once.
func Open(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return nil, err
	}
	app := core.NewBaseApp(core.BaseAppConfig{DataDir: dataDir})
	if err := app.Bootstrap(); err != nil {
		return nil, err
	}
	if err := app.RunAllMigrations(); err != nil {
		_ = app.ClearBootstrap()
		return nil, err
	}
	s := &Store{app: app}
	if _, err := app.FindRecordById("machine", "machine00000000"); recordMissing(err) {
		record := core.NewRecord(mustCollection(app, "machine"))
		record.Id = "machine00000000"
		record.Set("server_enabled", false)
		psk := tailcat.NewPresharedKey()
		pskText, err := psk.MarshalText()
		if err != nil {
			_ = app.ClearBootstrap()
			return nil, err
		}
		record.Set("server_psk", string(pskText))
		record.Set("server_region", 0)
		record.Set("identity_seed", hex.EncodeToString(NewIdentity().Seed()))
		if err := app.Save(record); err != nil {
			_ = app.ClearBootstrap()
			return nil, err
		}
	} else if err != nil {
		_ = app.ClearBootstrap()
		return nil, err
	}
	return s, nil
}

func (s *Store) Hosts() ([]Host, error) {
	recs, err := s.app.FindAllRecords("hosts")
	if err != nil {
		return nil, err
	}
	out := make([]Host, 0, len(recs))
	for _, r := range recs {
		out = append(out, Host{r.GetString("name"), r.GetString("address")})
	}
	return out, nil
}
func (s *Store) AddHost(h Host) error {
	r := core.NewRecord(mustCollection(s.app, "hosts"))
	r.Set("name", h.Name)
	r.Set("address", h.Address)
	return s.app.Save(r)
}
func (s *Store) DeleteHost(address string) error {
	r, err := s.app.FindFirstRecordByData("hosts", "address", address)
	if recordMissing(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return s.app.Delete(r)
}
func (s *Store) ServerEnabled() (bool, error) {
	r, e := s.machine()
	if e != nil {
		return false, e
	}
	return r.GetBool("server_enabled"), nil
}
func (s *Store) SetServerEnabled(v bool) error {
	r, e := s.machine()
	if e != nil {
		return e
	}
	r.Set("server_enabled", v)
	return s.app.Save(r)
}
func (s *Store) Serving(name string) (bool, error) {
	r, e := s.app.FindFirstRecordByData("launchers", "name", lower(name))
	if recordMissing(e) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	return r.GetBool("serving"), nil
}
func (s *Store) SetServing(name string, on bool) error {
	name = lower(name)
	r, e := s.app.FindFirstRecordByData("launchers", "name", name)
	if recordMissing(e) {
		r = core.NewRecord(mustCollection(s.app, "launchers"))
		r.Set("name", name)
	} else if e != nil {
		return e
	}
	r.Set("serving", on)
	return s.app.Save(r)
}
func (s *Store) LauncherConfig(launcher, hostAddress string) (map[string]string, error) {
	launcher, host := lower(launcher), ""
	if hostAddress != "" {
		h, err := s.app.FindFirstRecordByData("hosts", "address", hostAddress)
		if recordMissing(err) {
			return map[string]string{}, nil
		}
		if err != nil {
			return nil, err
		}
		host = h.Id
	}
	recs, err := s.app.FindRecordsByFilter("launcher_settings", "launcher = {:launcher} && host = {:host}", "", 0, 0, dbx.Params{"launcher": launcher, "host": host})
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(recs))
	for _, r := range recs {
		out[r.GetString("key")] = r.GetString("value")
	}
	return out, nil
}

// SetLauncherConfig replaces all settings for one launcher and host pair.
func (s *Store) SetLauncherConfig(launcher, hostAddress string, cfg map[string]string) error {
	launcher, host := lower(launcher), ""
	if hostAddress != "" {
		h, err := s.app.FindFirstRecordByData("hosts", "address", hostAddress)
		if err != nil {
			return err
		}
		host = h.Id
	}
	params := dbx.Params{"launcher": launcher, "host": host}
	return s.app.RunInTransaction(func(tx core.App) error {
		old, err := tx.FindRecordsByFilter("launcher_settings", "launcher = {:launcher} && host = {:host}", "", 0, 0, params)
		if err != nil {
			return err
		}
		for _, r := range old {
			if err := tx.Delete(r); err != nil {
				return err
			}
		}
		for k, v := range cfg {
			r := core.NewRecord(mustCollection(tx, "launcher_settings"))
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
func (s *Store) Identity() (Identity, error) {
	r, e := s.machine()
	if e != nil {
		return Identity{}, e
	}
	seed, e := hex.DecodeString(r.GetString("identity_seed"))
	if e != nil || len(seed) != ed25519.SeedSize {
		return Identity{}, errors.New("stored identity seed is malformed")
	}
	return IdentityFromSeed(seed), nil
}
func (s *Store) ServerParams() (tailcat.PresharedKey, tailcfg.DERPRegionID, error) {
	var p tailcat.PresharedKey
	r, e := s.machine()
	if e != nil {
		return p, 0, e
	}
	if e = p.UnmarshalText([]byte(r.GetString("server_psk"))); e != nil {
		return p, 0, e
	}
	return p, tailcfg.DERPRegionID(r.GetInt("server_region")), nil
}
func (s *Store) SaveRegion(id tailcfg.DERPRegionID) error {
	r, e := s.machine()
	if e != nil {
		return e
	}
	r.Set("server_region", int(id))
	return s.app.Save(r)
}
func (s *Store) Certs() ([][]byte, error) {
	recs, e := s.app.FindAllRecords("certs")
	if e != nil {
		return nil, e
	}
	out := make([][]byte, 0, len(recs))
	for _, r := range recs {
		b, e := hex.DecodeString(r.GetString("cert"))
		if e != nil {
			return nil, e
		}
		out = append(out, b)
	}
	return out, nil
}
func (s *Store) AddCert(cert []byte) error {
	v := hex.EncodeToString(cert)
	_, e := s.app.FindFirstRecordByData("certs", "cert", v)
	if e == nil {
		return nil
	}
	if !recordMissing(e) {
		return e
	}
	r := core.NewRecord(mustCollection(s.app, "certs"))
	r.Set("cert", v)
	return s.app.Save(r)
}
func (s *Store) DeleteCerts() error {
	recs, e := s.app.FindAllRecords("certs")
	if e != nil {
		return e
	}
	for _, r := range recs {
		if e = s.app.Delete(r); e != nil {
			return e
		}
	}
	return nil
}
