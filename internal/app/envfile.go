package app

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// envEntry is one NAME=value line of a data-folder settings file.
type envEntry struct {
	name, value string
}

var envNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// readEnvFile reads NAME=value lines. Blank lines, # comments, an
// "export " prefix and quotes around a value are allowed so the file can be
// edited by hand; lines that are not NAME=value are skipped. A missing file
// is empty. Errors never include the file's contents (it may hold keys).
func readEnvFile(path string) ([]envEntry, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var out []envEntry
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, ok := strings.Cut(strings.TrimPrefix(line, "export "), "=")
		name, value = strings.TrimSpace(name), strings.TrimSpace(value)
		if !ok || !envNameRE.MatchString(name) {
			continue
		}
		if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
			value = value[1 : len(value)-1]
		}
		out = append(out, envEntry{name, value})
	}
	return out, nil
}

// lookupEnv returns the last value given for name.
func lookupEnv(entries []envEntry, name string) (string, bool) {
	value, found := "", false
	for _, e := range entries {
		if e.name == name {
			value, found = e.value, true
		}
	}
	return value, found
}

// writeEnvFile replaces the file at once (a temporary file renamed over it),
// readable only by the current user. With no entries the file is removed.
func writeEnvFile(path string, header []string, entries []envEntry) error {
	if len(entries) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove %s: %w", path, err)
		}
		return nil
	}
	var b strings.Builder
	for _, h := range header {
		b.WriteString("# " + h + "\n")
	}
	for _, e := range entries {
		b.WriteString(e.name + "=" + e.value + "\n")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".settings-*.tmp") // created 0600
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	defer os.Remove(tmp.Name()) // a no-op after the rename
	if _, err := tmp.WriteString(b.String()); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// envName turns an ID into the NAME part of a line: "e2efake2" → "E2EFAKE2".
func envName(id string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r - 'a' + 'A'
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		}
		return '_'
	}, id)
}
