package planstore

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	homeKeyName = "master.key"
	plansRel    = ".reasonix/plans"
	keysRel     = ".reasonix/keys"

	// DefaultMaxEntries caps the number of write_scope and change_manifest
	// entries plan_submit accepts per list (shared by both, per UAT). The cap
	// is configurable per store (Store.MaxEntries) so tests inject a small
	// value; 50 is the production default. Rationale (Sprint 10 registry
	// entry 4): models occasionally submit absurdly large lists — a directory
	// prefix in the scope already covers everything below it recursively, so a
	// huge list is a smell and is hard to review.
	DefaultMaxEntries = 50
)

// Store is the encrypted plan store bound to one workspace. It holds the
// decrypted per-project key in memory only; the project key ciphertext lives
// under <ws>/.reasonix/keys/<hash>.key.enc and the master key lives in the
// user-level Reasonix home, outside the model's bash sandbox.
type Store struct {
	wsRoot     string
	homeDir    string
	projectKey []byte
	plansDir   string
	keysDir    string

	// MaxEntries caps plan_submit's write_scope and change_manifest entry
	// counts (default DefaultMaxEntries). Exposed for tests to inject a small
	// cap; production always gets the default via Open.
	MaxEntries int
}

// masterKeyPath resolves <reasonix home>/master.key. The resolution mirrors
// config.reasonixHomeDir (REASONIX_HOME → %APPDATA%/reasonix → ~/.reasonix)
// without importing config, keeping planstore self-contained and test-isolable.
func masterKeyPath() string {
	if dir := strings.TrimSpace(os.Getenv("REASONIX_HOME")); dir != "" {
		return filepath.Join(dir, homeKeyName)
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	if runtime.GOOS == "windows" {
		return filepath.Join(home, "AppData", "Roaming", "reasonix", homeKeyName)
	}
	return filepath.Join(home, ".reasonix", homeKeyName)
}

// HomeDir returns the resolved user-level Reasonix home. The bash sandbox in
// strict plan mode forbids reading this directory so the master key stays out
// of reach of the model.
func HomeDir() string {
	p := masterKeyPath()
	if p == "" {
		return ""
	}
	return filepath.Dir(p)
}

// PlansDir returns the plan artifact directory of a workspace, exposed so the
// bash sandbox can forbid-read it in strict plan mode.
func PlansDir(wsRoot string) string {
	return filepath.Join(wsRoot, plansRel)
}

// Open loads (or creates on first use) the master key and the workspace's
// project key, and prepares the .reasonix layout. It fails closed on any
// corruption instead of silently rebuilding user-level key material.
func Open(wsRoot string) (*Store, error) {
	ws, err := filepath.Abs(filepath.Clean(wsRoot))
	if err != nil {
		return nil, fmt.Errorf("planstore: workspace root: %w", err)
	}
	mkPath := masterKeyPath()
	if mkPath == "" {
		return nil, fmt.Errorf("planstore: cannot resolve Reasonix home (REASONIX_HOME unset and no user home)")
	}
	master, err := loadOrCreateMasterKey(mkPath)
	if err != nil {
		return nil, err
	}
	keysDir := filepath.Join(ws, keysRel)
	plansDir := filepath.Join(ws, plansRel)
	if err := os.MkdirAll(keysDir, 0o700); err != nil {
		return nil, fmt.Errorf("planstore: create keys dir: %w", err)
	}
	if err := os.MkdirAll(plansDir, 0o700); err != nil {
		return nil, fmt.Errorf("planstore: create plans dir: %w", err)
	}
	projectKey, err := loadOrCreateProjectKey(keysDir, ws, master)
	if err != nil {
		return nil, err
	}
	return &Store{
		wsRoot:     ws,
		homeDir:    filepath.Dir(mkPath),
		projectKey: projectKey,
		plansDir:   plansDir,
		keysDir:    keysDir,
		MaxEntries: DefaultMaxEntries,
	}, nil
}

// Close releases nothing today (no external resources); it exists so callers
// have an explicit teardown point before reopening the same workspace.
func (s *Store) Close() error {
	return nil
}

// WorkspaceRoot returns the absolute workspace root the store is bound to.
func (s *Store) WorkspaceRoot() string {
	return s.wsRoot
}

func loadOrCreateMasterKey(path string) ([]byte, error) {
	if data, err := os.ReadFile(path); err == nil {
		key, err := hex.DecodeString(strings.TrimSpace(string(data)))
		if err != nil || len(key) != 32 {
			return nil, fmt.Errorf("planstore: master key %s is corrupt (want 32-byte hex); refusing to overwrite user-level data", path)
		}
		return key, nil
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("planstore: read master key: %w", err)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("planstore: generate master key: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("planstore: create home dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(hex.EncodeToString(key)+"\n"), 0o600); err != nil {
		return nil, fmt.Errorf("planstore: write master key: %w", err)
	}
	return key, nil
}

func loadOrCreateProjectKey(keysDir, wsRoot string, master []byte) ([]byte, error) {
	sum := sha256.Sum256([]byte(wsRoot))
	keyPath := filepath.Join(keysDir, hex.EncodeToString(sum[:8])+".key.enc")
	if data, err := os.ReadFile(keyPath); err == nil {
		key, err := decryptGCM(master, strings.TrimSpace(string(data)))
		if err != nil {
			return nil, fmt.Errorf("planstore: decrypt project key %s: %w", keyPath, err)
		}
		return key, nil
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("planstore: read project key: %w", err)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("planstore: generate project key: %w", err)
	}
	enc, err := encryptGCM(master, key)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(keyPath, []byte(enc+"\n"), 0o600); err != nil {
		return nil, fmt.Errorf("planstore: write project key: %w", err)
	}
	return key, nil
}
