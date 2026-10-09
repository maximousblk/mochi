package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagedSSHConfigTogglePreservesUserConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".ssh", "config")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	const userConfig = "Host personal\n    HostName example.com\n"
	if err := os.WriteFile(path, []byte(userConfig), 0600); err != nil {
		t.Fatal(err)
	}
	host := Host{Name: "lab machine", Address: "tcExampleAddress"}
	connector := NewSSHConfigConnector(path, nil)
	if err := connector.Enable(host); err != nil {
		t.Fatalf("enable: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, userConfig) || !strings.Contains(text, "BEGIN MOCHI MANAGED") || !strings.Contains(text, "Host lab-machine") {
		t.Fatalf("managed config missing or user config changed: %q", text)
	}
	executable, err := currentExecutablePath()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "ProxyCommand "+quoteProxyExecutable(executable)+" connector-ssh-proxy "+host.Address+" %p") {
		t.Fatalf("ProxyCommand does not use current absolute executable path: %q", text)
	}
	if err := connector.Disable(host); err != nil {
		t.Fatalf("disable: %v", err)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != userConfig {
		t.Fatalf("user config was not restored: %q", string(data))
	}
}

func TestRefreshRewritesExistingManagedBlockToCurrentExecutable(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".ssh", "config")
	host := Host{Name: "lab", Address: "tcExampleAddress"}
	begin, end := managedBlockMarkers(host)
	old := managedBlock(host, begin, end, "/old/path/mochi")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(old), 0600); err != nil {
		t.Fatal(err)
	}
	connector := NewSSHConfigConnector(path, nil)
	if err := connector.Refresh([]Host{host}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	executable, err := currentExecutablePath()
	if err != nil {
		t.Fatal(err)
	}
	if !connector.Enabled(host) || strings.Contains(string(data), "/old/path/mochi") || !strings.Contains(string(data), quoteProxyExecutable(executable)) {
		t.Fatalf("managed block was not refreshed: %q", data)
	}
}
