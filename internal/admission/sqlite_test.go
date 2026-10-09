package admission

import (
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ashwin-y/mochi/internal/state"
)

// TestMain lets the test binary double as the second Mochi process: the GUI
// writes the clients table while connector-ssh-proxy processes, each started
// fresh, open the same database file.
func TestMain(m *testing.M) {
	if mode := os.Getenv("MOCHI_TEST_HELPER"); mode != "" {
		os.Exit(proxyProcess(mode, os.Getenv("MOCHI_TEST_DB")))
	}
	os.Exit(m.Run())
}

func openMode(mode, path string) (*sql.DB, error) {
	if mode == "plain" { // how Mochi opens it today
		return sql.Open("sqlite", path)
	}
	return state.OpenDB(path)
}

// proxyProcess is what a connector-ssh-proxy start does to the database: open
// it, make sure the schema exists, apply the embedded bootstrap settings (a
// write), and read what it needs. It prints how many of those failed.
func proxyProcess(mode, path string) int {
	failures, runs := 0, 60
	for range runs {
		db, err := openMode(mode, path)
		if err == nil {
			_, err = db.Exec(schema)
			if err == nil {
				_, err = db.Exec(`CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT NOT NULL)`)
			}
			if err == nil {
				_, err = db.Exec(`INSERT INTO settings (key, value) VALUES ('ssh_config_path', '') ON CONFLICT(key) DO UPDATE SET value = excluded.value`)
			}
			if err == nil {
				var n int
				err = db.QueryRow(`SELECT COUNT(*) FROM clients`).Scan(&n)
			}
			db.Close()
		}
		if err != nil {
			failures++
			fmt.Fprintln(os.Stderr, "proxy:", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	fmt.Printf("failures=%d runs=%d\n", failures, runs)
	return 0
}

// contend runs the GUI's writers in this process against proxy starts in a
// child process, with the database opened per mode, and returns the failures
// seen on each side.
func contend(t *testing.T, mode string) (parent, child int) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mochi.db")
	db, err := openMode(mode, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(schema); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), "MOCHI_TEST_HELPER="+mode, "MOCHI_TEST_DB="+path)
	var out strings.Builder
	cmd.Stdout = &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	var wg sync.WaitGroup
	var mu sync.Mutex
	stop := make(chan struct{})
	for w := range 4 { // the hook, the proof handler, the sweeper, the UI
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				now := time.Now().Unix()
				_, err := db.Exec(`INSERT INTO clients (node_key, state, reason, expires, first_seen, last_seen) VALUES (?, 'rejected', 'x', ?, ?, ?)
					ON CONFLICT(node_key) DO UPDATE SET last_seen = excluded.last_seen`, fmt.Sprintf("nodekey:%d-%d", w, i%50), now+60, now, now)
				if err != nil {
					mu.Lock()
					parent++
					mu.Unlock()
				}
				time.Sleep(time.Millisecond)
			}
		}()
	}
	if err := <-done; err != nil {
		t.Fatalf("child: %v", err)
	}
	close(stop)
	wg.Wait()
	for _, f := range strings.Fields(out.String()) {
		if v, ok := strings.CutPrefix(f, "failures="); ok {
			child, _ = strconv.Atoi(v)
		}
	}
	return parent, child
}

func TestSQLiteTwoProcesses(t *testing.T) {
	// For comparison: the open Mochi does today, which fails as soon as two
	// writers meet. Logged, not asserted: it only shows the test can see the problem.
	p, c := contend(t, "plain")
	t.Logf("poc: plain sql.Open(path): %d writer errors in the GUI process, %d in the proxy process", p, c)

	p, c = contend(t, "opendb")
	t.Logf("poc: OpenDB(path):         %d writer errors in the GUI process, %d in the proxy process", p, c)
	if p != 0 || c != 0 {
		t.Fatalf("OpenDB lost writes under two-process contention: GUI %d, proxy %d", p, c)
	}
}
