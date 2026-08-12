package file

import (
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"testing"

	"github.com/shibernetes/kem-agent/checkpoint"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.CreateDirectory {
		t.Error("directories are created by default, want the store to fail instead")
	}
	if cfg.DirectoryMode != 0o700 {
		t.Errorf("got directory mode %o, want 700", cfg.DirectoryMode)
	}
}

func TestConfigAccepts(t *testing.T) {
	cases := map[string]Config{
		"path only":             {Type: TypeName, Path: "/var/lib/kem-agent/checkpoint.json"},
		"directory to create":   {Type: TypeName, Path: "/state/checkpoint.json", CreateDirectory: true, DirectoryMode: 0o700},
		"wider directory mode":  {Type: TypeName, Path: "/state/checkpoint.json", CreateDirectory: true, DirectoryMode: 0o755},
		"unused directory mode": {Type: TypeName, Path: "/state/checkpoint.json", DirectoryMode: 700},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			if err := cfg.Validate(); err != nil {
				t.Errorf("the configuration was rejected, want it accepted: %v", err)
			}
		})
	}
}

func TestConfigRejects(t *testing.T) {
	cases := map[string]Config{
		"no path":                   {},
		"no mode to create with":    {Path: "/state/checkpoint.json", CreateDirectory: true},
		"mode written in decimal":   {Path: "/state/checkpoint.json", CreateDirectory: true, DirectoryMode: 700},
		"mode with setuid":          {Path: "/state/checkpoint.json", CreateDirectory: true, DirectoryMode: 0o4700},
		"mode with a file type bit": {Path: "/state/checkpoint.json", CreateDirectory: true, DirectoryMode: os.ModeDir | 0o700},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			if err := cfg.Validate(); err == nil {
				t.Error("the configuration was accepted, want it rejected")
			}
		})
	}
}

func TestLoadReportsMissingState(t *testing.T) {
	_, err := New(Config{Path: newPath(t)}).Load(context.Background())
	if !errors.Is(err, checkpoint.ErrNotFound) {
		t.Errorf("got error %v, want ErrNotFound", err)
	}
}

func TestLoadReportsUnreadableState(t *testing.T) {
	path := newPath(t)
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("failed to write state file: %v", err)
	}
	_, err := New(Config{Path: path}).Load(context.Background())
	if !errors.Is(err, checkpoint.ErrCorrupt) {
		t.Errorf("got error %v, want ErrCorrupt", err)
	}
}

func TestLoadFailsWithoutDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "checkpoint.json")

	_, err := New(Config{Path: path}).Load(context.Background())
	if err == nil {
		t.Fatal("got no error, want a missing directory to fail")
	}
	// A directory that does not exist can never be written to, so
	// it must report a distinct error from a missing state file.
	if errors.Is(err, checkpoint.ErrNotFound) {
		t.Error("got ErrNotFound, want a missing directory reported as its own failure")
	}
}

func TestLoadCreatesDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state", "v1")

	cfg := DefaultConfig()
	cfg.Path = filepath.Join(dir, "checkpoint.json")
	cfg.CreateDirectory = true

	_, err := New(cfg).Load(context.Background())
	if !errors.Is(err, checkpoint.ErrNotFound) {
		t.Errorf("got error %v, want ErrNotFound", err)
	}
	if got := fileMode(t, dir); got != defaultDirectoryMode {
		t.Errorf("got dir mode %o, want %o", got, defaultDirectoryMode)
	}
}

func TestSaveThenLoad(t *testing.T) {
	var (
		store = New(Config{Path: newPath(t)})
		want  = newState()
	)
	if err := store.Save(context.Background(), want); err != nil {
		t.Fatalf("failed to save state: %v", err)
	}
	state, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("failed to load state: %v", err)
	}
	if !maps.Equal(state.Watches, want.Watches) {
		t.Errorf("got watches %v, want %v", state.Watches, want.Watches)
	}
}

func TestSaveWritesPrivateFile(t *testing.T) {
	cases := map[string]struct {
		mode     os.FileMode
		existing bool
	}{
		"new file":           {},
		"world readable one": {mode: 0o644, existing: true},
		"unreadable one":     {mode: 0o000, existing: true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			path := newPath(t)
			if tc.existing {
				if err := os.WriteFile(path, []byte("{}"), tc.mode); err != nil {
					t.Fatalf("failed to write file: %v", err)
				}
			}
			if err := New(Config{Path: path}).Save(context.Background(), newState()); err != nil {
				t.Fatalf("failed to save state: %v", err)
			}
			if got := fileMode(t, path); got != 0o600 {
				t.Errorf("got mode %o, want 600", got)
			}
		})
	}
}

func TestSaveLeavesNoTemporaryFile(t *testing.T) {
	var (
		dir   = t.TempDir()
		store = New(Config{Path: filepath.Join(dir, "checkpoint.json")})
	)
	for range 2 {
		if err := store.Save(context.Background(), newState()); err != nil {
			t.Fatalf("failed to save state: %v", err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("failed to read directory: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("got %d files, want only the state file", len(entries))
	}
}

func TestSaveCleansUpAfterFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "checkpoint.json")

	// A rename cannot replace a directory, so creating one
	// using the path of the state file fails the rename of
	// the temporary file.
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatalf("failed to create state directory: %v", err)
	}
	if err := New(Config{Path: path}).Save(context.Background(), newState()); err == nil {
		t.Fatal("got no error, want the save to fail")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("failed to read directory: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("got %d files, want the temporary one removed", len(entries))
	}
}

func TestSaveFailsOnMissingDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "checkpoint.json")

	if err := New(Config{Path: path}).Save(context.Background(), newState()); err == nil {
		t.Error("got no error, want a failed save for a missing directory")
	}
}

func newPath(t *testing.T) string {
	t.Helper()

	return filepath.Join(t.TempDir(), "checkpoint.json")
}

func newState() checkpoint.State {
	return checkpoint.State{Watches: map[string]string{"": "100", "team-a": "42"}}
}

func fileMode(t *testing.T, path string) os.FileMode {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("failed to stat file %q: %v", path, err)
	}
	return info.Mode().Perm()
}
