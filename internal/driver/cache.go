package driver

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/sarora/herdr-godex-cost/internal/codex"
)

// Identifies the refresh cache and rollout fingerprint format.
const CacheVersion = 14

// Loads and stores refresh results for one session fingerprint.
type Cache struct {
	Path        string
	SessionID   string
	Fingerprint string
}

func NewCache(path, sessionId, fingerprint string) *Cache {
	return &Cache{
		Path:        path,
		SessionID:   sessionId,
		Fingerprint: fingerprint,
	}
}

type cacheEnvelope struct {
	Version     int             `json:"version"`
	SessionID   string          `json:"session_id"`
	Fingerprint string          `json:"fingerprint"`
	Value       *codex.Estimate `json:"value"`
}

var ErrCacheMiss = errors.New("refresh cache miss")

// Loads a cached value only when its version, session, and rollout
// fingerprint all match. Missing, stale, or malformed cache data is a miss.
func (cache Cache) Read() (*codex.Estimate, error) {
	if cache.Path == "" {
		return nil, ErrCacheMiss
	}
	contents, err := os.ReadFile(cache.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrCacheMiss
	}
	if err != nil {
		return nil, fmt.Errorf("read refresh cache %q: %w", cache.Path, err)
	}

	var cached cacheEnvelope
	if err := json.Unmarshal(contents, &cached); err != nil ||
		cached.Version != CacheVersion ||
		cached.SessionID != cache.SessionID ||
		cached.Fingerprint != cache.Fingerprint ||
		cached.Value == nil {
		return nil, ErrCacheMiss
	}
	return cached.Value, nil
}

// Writes a versioned value beside its destination and renames
// it into place so readers never observe a partially encoded cache file.
func (cache Cache) WriteAtomic(value codex.Estimate) error {
	if cache.Path == "" {
		return nil
	}
	cached := cacheEnvelope{
		Version:     CacheVersion,
		SessionID:   cache.SessionID,
		Fingerprint: cache.Fingerprint,
		Value:       &value,
	}

	directory := filepath.Dir(cache.Path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create refresh cache directory %q: %w", directory, err)
	}
	temporary, err := os.CreateTemp(directory, ".codex-cost-cache-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary refresh cache in %q: %w", directory, err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)

	encoder := json.NewEncoder(temporary)
	if err := encoder.Encode(cached); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write refresh cache: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close refresh cache: %w", err)
	}
	if err := os.Rename(temporaryPath, cache.Path); err != nil {
		return fmt.Errorf("replace refresh cache %q: %w", cache.Path, err)
	}
	return nil
}
