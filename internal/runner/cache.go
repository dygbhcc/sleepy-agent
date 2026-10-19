package runner

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Fingerprinter is implemented by a Scripter that can say what it is: the
// model, the settings and the prompt version. Without one the worker has
// nothing safe to key a cache on, so it does not cache.
type Fingerprinter interface {
	Fingerprint() string
}

// ScriptCache keeps scripts the QA gate accepted, by input hash, so the same
// input is never paid for twice. Nothing rejected is ever put in it. All
// methods are best effort from the worker's side: an error is a miss.
type ScriptCache interface {
	Get(key string) (Script, bool, error)
	Put(key string, s Script) error
	Delete(key string) error
}

// CacheKey is the input hash of a script: the producer's fingerprint and the
// topic with its whitespace normalised. Case is kept, the model sees it.
func CacheKey(fingerprint, topic string) string {
	h := sha256.New()
	h.Write([]byte(fingerprint))
	h.Write([]byte{0})
	h.Write([]byte(strings.Join(strings.Fields(topic), " ")))
	return hex.EncodeToString(h.Sum(nil))
}

// MemoryCache is a ScriptCache that lives as long as the process.
type MemoryCache struct {
	mu      sync.Mutex
	entries map[string]Script
}

// NewMemoryCache returns an empty cache.
func NewMemoryCache() *MemoryCache { return &MemoryCache{entries: map[string]Script{}} }

// Get implements ScriptCache.
func (c *MemoryCache) Get(key string) (Script, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.entries[key]
	return s, ok, nil
}

// Put implements ScriptCache.
func (c *MemoryCache) Put(key string, s Script) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = s
	return nil
}

// Delete implements ScriptCache.
func (c *MemoryCache) Delete(key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, key)
	return nil
}

// FileCache is a ScriptCache kept in one JSON file, so a second process, or
// the same one after a restart, finds what the first paid for. Writes replace
// the file atomically. It is meant for one process at a time, like store.File.
type FileCache struct {
	mu      sync.Mutex
	path    string
	entries map[string]Script
}

type cacheEntry struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

// OpenFileCache opens the cache file, creating an empty cache when it does not
// exist. A file that is not a cache is an error, not an empty cache: nothing
// is overwritten silently.
func OpenFileCache(path string) (*FileCache, error) {
	c := &FileCache{path: path, entries: map[string]Script{}}
	b, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return c, nil
	case err != nil:
		return nil, fmt.Errorf("script cache: %w", err)
	}
	var raw map[string]cacheEntry
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("script cache: %s is not a cache file: %w", path, err)
	}
	for k, e := range raw {
		c.entries[k] = Script{Title: e.Title, Body: e.Body}
	}
	return c, nil
}

// Get implements ScriptCache.
func (c *FileCache) Get(key string) (Script, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.entries[key]
	return s, ok, nil
}

// Put implements ScriptCache.
func (c *FileCache) Put(key string, s Script) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = s
	return c.save()
}

// Delete implements ScriptCache.
func (c *FileCache) Delete(key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.entries[key]; !ok {
		return nil
	}
	delete(c.entries, key)
	return c.save()
}

func (c *FileCache) save() error {
	raw := make(map[string]cacheEntry, len(c.entries))
	for k, s := range c.entries {
		raw[k] = cacheEntry{Title: s.Title, Body: s.Body}
	}
	b, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(c.path), ".cache-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) //nolint:errcheck // gone after the rename, a leftover temp file is harmless
	if _, err := tmp.Write(b); err != nil {
		tmp.Close() //nolint:errcheck // the write error is the one to report
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), c.path)
}
