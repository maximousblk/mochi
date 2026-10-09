package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func defaultSSHConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ssh", "config"), nil
}

func statePath() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "mochi", "mochi.db"), nil
}

func legacyConfigPath() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "mochi", "config.json"), nil
}

func managedBlockMarkers(host Host) (string, string) {
	hash := sha256.Sum256([]byte(host.Address))
	id := hex.EncodeToString(hash[:])[:16]
	return "# BEGIN MOCHI MANAGED " + id, "# END MOCHI MANAGED " + id
}

func currentExecutablePath() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate Mochi executable: %w", err)
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return "", err
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return "", fmt.Errorf("resolve Mochi executable path: %w", err)
	}
	return filepath.Clean(executable), nil
}

func managedBlock(host Host, begin, end, executable string) string {
	return fmt.Sprintf("%s\nHost %s\n    HostName %s\n    ProxyCommand %s connector-ssh-proxy %s %%p\n%s\n", begin, sshAlias(host.Name), host.Address, quoteProxyExecutable(executable), host.Address, end)
}

func quoteProxyExecutable(executable string) string {
	if os.PathSeparator == '\\' {
		return `"` + strings.ReplaceAll(executable, `"`, `\"`) + `"`
	}
	return "'" + strings.ReplaceAll(executable, "'", "'\\''") + "'"
}

func removeManagedBlock(text, begin, end string) string {
	for {
		start := strings.Index(text, begin)
		if start < 0 {
			return text
		}
		finish := strings.Index(text[start:], end)
		if finish < 0 {
			return text[:start]
		}
		finish += start + len(end)
		if finish < len(text) && text[finish] == '\n' {
			finish++
		}
		if start >= 2 && text[start-1] == '\n' && text[start-2] == '\n' {
			start--
		}
		text = text[:start] + text[finish:]
	}
}

func sshAlias(name string) string {
	fields := strings.Fields(name)
	if len(fields) == 0 {
		return "mochi-host"
	}
	return strings.Join(fields, "-")
}
