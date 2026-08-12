package file

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/shibernetes/kem-agent/checkpoint"
	"github.com/shibernetes/kem-agent/config/diag"
)

// TypeName is the value of the type key that selects this implementation.
const (
	TypeName = "file"
)

const (
	defaultDirectoryMode = 0o700
)

var _ checkpoint.Store = (*Store)(nil)

// A Store persists a checkpoint state in a JSON-formatted file.
// Saves are atomic, written to a temporary file and renamed into place.
type Store struct {
	path      string
	createDir bool
	dirMode   os.FileMode
}

// Config defines the configuration for a [Store].
type Config struct {
	Type string `yaml:"type"`
	Path string `yaml:"path"`

	// CreateDirectory creates the directory holding the state file rather
	// than failing when it does not exist.
	CreateDirectory bool `yaml:"create_directory,omitempty"`

	// DirectoryMode is the permission a created directory takes, and is
	// written in octal, with the leading zero.
	DirectoryMode os.FileMode `yaml:"directory_mode,omitempty"`
}

// DefaultConfig returns the default configuration.
func DefaultConfig() Config {
	return Config{Type: TypeName, DirectoryMode: defaultDirectoryMode}
}

// Validate validates the configuration.
func (c Config) Validate() error {
	if err := diag.Required(c); err != nil {
		return err
	}
	if c.CreateDirectory {
		// Anything outside the permission bits is a mode bit rather than a
		// permission, which is also where a mode written without its leading
		// zero lands, 700 in decimal being 01274.
		if c.DirectoryMode&^os.ModePerm != 0 || c.DirectoryMode == 0 {
			return diag.Pathf("directory_mode", "invalid directory_mode %#o, must be a permission", c.DirectoryMode)
		}
	}
	return nil
}

// New returns a store backed by a file.
func New(cfg Config) *Store {
	return &Store{
		path:      cfg.Path,
		createDir: cfg.CreateDirectory,
		dirMode:   cfg.DirectoryMode,
	}
}

// Load reads and decodes the state. It returns [checkpoint.ErrNotFound]
// when the file does not exist, but fails when its directory does not.
func (s *Store) Load(_ context.Context) (checkpoint.State, error) {
	b, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		if err := s.ensureDirectory(); err != nil {
			return checkpoint.State{}, err
		}
		return checkpoint.State{}, checkpoint.ErrNotFound
	}
	if err != nil {
		return checkpoint.State{}, fmt.Errorf("failed to read file %q: %w", s.path, err)
	}
	var state checkpoint.State

	if err := json.Unmarshal(b, &state); err != nil {
		return checkpoint.State{}, fmt.Errorf("%w: %w", checkpoint.ErrCorrupt, err)
	}
	return state, nil
}

// Save writes the state to a temporary file in the same directory
// and renames it over the target file, so a reader never sees a
// partial write.
func (s *Store) Save(_ context.Context, state checkpoint.State) error {
	b, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode checkpoint state: %w", err)
	}
	// The temporary file is created with perms 0600 and the
	// rename carries that mode onto the state file.
	tmp, err := os.CreateTemp(filepath.Dir(s.path), filepath.Base(s.path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temporary file: %w", err)
	}
	tmpName := tmp.Name()

	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}()
	if _, err := tmp.Write(b); err != nil {
		return fmt.Errorf("failed to write temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("failed to sync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("failed to close temp file: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("failed to rename file %q: %w", s.path, err)
	}
	// Sync the directory, so the rename is durable across a crash.
	dir, err := os.Open(filepath.Dir(s.path))
	if err != nil {
		return fmt.Errorf("failed to open directory: %w", err)
	}
	defer func() {
		_ = dir.Close()
	}()
	if err := dir.Sync(); err != nil {
		return fmt.Errorf("failed to sync directory: %w", err)
	}
	return nil
}

// ensureDirectory checks that the file's directory exists,
// or creates it if the configuration asks for it.
func (s *Store) ensureDirectory() error {
	dir := filepath.Dir(s.path)

	_, err := os.Stat(dir)
	if err == nil {
		return nil
	}
	// Only an absent directory is created. A stat refused for
	// any other reason is reported as-is.
	if !s.createDir || !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("failed to stat directory %q: %w", dir, err)
	}
	// The mode is left to the umask rather than restored with
	// a chmod afterward, so that a tighter one is applied.
	if err := os.MkdirAll(dir, s.dirMode); err != nil {
		return fmt.Errorf("failed to create directory %q: %w", dir, err)
	}
	return nil
}
