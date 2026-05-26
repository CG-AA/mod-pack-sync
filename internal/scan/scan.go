// Package scan walks an instance directory, applying an exclude list, and
// produces hashed file entries.
package scan

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/cg-aa/mod-pack-sync/internal/atomicio"
	"github.com/cg-aa/mod-pack-sync/internal/manifest"
)

// DefaultExcludes are paths that should never be synced: world saves, logs,
// caches, per-user preferences, and the sync tool's own files. Entries ending
// in "/" match a directory and everything under it; entries containing "*" are
// matched as globs against both the full relative path and the base name.
var DefaultExcludes = []string{
	// tool state and binaries (the tool lives in the instance root)
	".modpack-sync/",
	".modpack-sync-tmp-*", // half-written atomic-write temps a crash may leak
	"modpack-send*",
	"modpack-receive*",
	"*.exe",
	// world data and per-machine state
	"saves/",
	"backups/",
	"logs/",
	"crash-reports/",
	"screenshots/",
	"local/",
	".git/",
	".fabric/",
	".mixin.out/",
	"usercache.json",
	"usernamecache.json",
	"servers.dat",
	"servers.dat_old",
	"options.txt",
	"optionsof.txt",
	"optionsshaders.txt",
	"*.log",
	"*.log.gz",
	"hs_err_pid*",
}

// SelfExcludes returns exclude patterns for the currently running executable's
// own file name, so a renamed binary still never syncs itself.
func SelfExcludes() []string {
	exe, err := os.Executable()
	if err != nil {
		return nil
	}
	return []string{filepath.Base(exe)}
}

// Excluder decides whether an instance-relative path is excluded.
type Excluder struct {
	dirs  []string // normalized, with trailing slash
	exact map[string]bool
	globs []string
}

// NewExcluder builds an Excluder from a list of patterns.
func NewExcluder(patterns []string) *Excluder {
	e := &Excluder{exact: map[string]bool{}}
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		if p == "" || strings.HasPrefix(p, "#") {
			continue
		}
		p = filepath.ToSlash(p)
		switch {
		case strings.HasSuffix(p, "/"):
			e.dirs = append(e.dirs, p)
		case strings.Contains(p, "*"):
			e.globs = append(e.globs, p)
		default:
			e.exact[p] = true
		}
	}
	return e
}

// Match reports whether rel (forward-slash, instance-relative) is excluded.
func (e *Excluder) Match(rel string) bool {
	rel = filepath.ToSlash(rel)
	if e.exact[rel] {
		return true
	}
	for _, d := range e.dirs {
		if rel == strings.TrimSuffix(d, "/") || strings.HasPrefix(rel, d) {
			return true
		}
	}
	base := path(rel)
	for _, g := range e.globs {
		if ok, _ := filepath.Match(g, rel); ok {
			return true
		}
		if ok, _ := filepath.Match(g, base); ok {
			return true
		}
	}
	return false
}

func path(rel string) string {
	if i := strings.LastIndex(rel, "/"); i >= 0 {
		return rel[i+1:]
	}
	return rel
}

// Walk scans root, returning hashed entries for every non-excluded regular
// file, sorted by path. Symlinks are skipped.
func Walk(root string, ex *Excluder) ([]manifest.FileEntry, error) {
	return WalkCached(root, ex, nil)
}

// WalkCached is Walk with an optional hash cache: a file whose size and mtime
// match a cached entry reuses the stored hash instead of re-reading it, which is
// a large speedup for packs with thousands of small files. Pass nil to hash
// everything. Hashing runs on a bounded worker pool (tiny-file hashing is
// IO/syscall-bound, so parallelism hides latency). The cache is updated in place;
// the caller is responsible for persisting it with (*HashCache).Save.
func WalkCached(root string, ex *Excluder, cache *HashCache) ([]manifest.FileEntry, error) {
	type job struct {
		abs, rel string
		size     int64
		modNs    int64
	}
	var jobs []job
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return rerr
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			if ex.Match(rel + "/") {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil // skip symlinks, sockets, etc.
		}
		if ex.Match(rel) {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return ierr
		}
		jobs = append(jobs, job{abs: p, rel: rel, size: info.Size(), modNs: info.ModTime().UnixNano()})
		return nil
	})
	if err != nil {
		return nil, err
	}

	entries := make([]manifest.FileEntry, len(jobs))
	workers := runtime.GOMAXPROCS(0)
	if workers > 8 {
		workers = 8
	}
	if workers < 1 {
		workers = 1
	}
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	var firstErr error
	var errMu sync.Mutex
	for i := range jobs {
		jb := jobs[i]
		if cache != nil {
			if h, ok := cache.get(jb.rel, jb.size, jb.modNs); ok {
				entries[i] = manifest.FileEntry{Path: jb.rel, Size: jb.size, Hash: h}
				continue
			}
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, jb job) {
			defer wg.Done()
			defer func() { <-sem }()
			h, size, herr := HashFile(jb.abs)
			if herr != nil {
				errMu.Lock()
				if firstErr == nil {
					firstErr = herr
				}
				errMu.Unlock()
				return
			}
			entries[i] = manifest.FileEntry{Path: jb.rel, Size: size, Hash: h}
			if cache != nil {
				cache.put(jb.rel, size, jb.modNs, h)
			}
		}(i, jb)
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return entries, nil
}

// HashFile returns the sha256 (lowercase hex) and size of the file at p.
func HashFile(p string) (string, int64, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

type cacheEntry struct {
	Size  int64  `json:"size"`
	ModNs int64  `json:"mod_ns"`
	Hash  string `json:"hash"`
}

// HashCache memoizes file hashes keyed by (instance-relative path, size, mtime),
// so an unchanged file is not re-read on the next scan — a large speedup for
// packs with thousands of small files. A change in size or mtime invalidates the
// entry. Reads come from the previously-loaded set; every file seen during a scan
// is recorded into a fresh set, so Save persists only currently-present files and
// entries for deleted files are pruned automatically. Safe for concurrent use.
type HashCache struct {
	path  string
	mu    sync.Mutex
	old   map[string]cacheEntry // loaded from disk, read-only during a scan
	fresh map[string]cacheEntry // files observed this scan, persisted by Save
}

// LoadHashCache reads the cache at path; a missing or corrupt file yields an
// empty (but usable) cache.
func LoadHashCache(path string) *HashCache {
	c := NewHashCache(path)
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &c.old)
	}
	return c
}

// NewHashCache returns an empty cache bound to path: nothing is reused (every
// file is re-hashed), but Save still persists the fresh results. Use it for a
// forced re-hash that keeps the cache warm for next time.
func NewHashCache(path string) *HashCache {
	return &HashCache{path: path, old: map[string]cacheEntry{}, fresh: map[string]cacheEntry{}}
}

func (c *HashCache) get(rel string, size, modNs int64) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.old[rel]; ok && e.Size == size && e.ModNs == modNs {
		c.fresh[rel] = e // carry the still-valid entry into the persisted set
		return e.Hash, true
	}
	return "", false
}

func (c *HashCache) put(rel string, size, modNs int64, hash string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.fresh[rel] = cacheEntry{Size: size, ModNs: modNs, Hash: hash}
}

// Save writes the freshly-observed entries to the cache path atomically.
func (c *HashCache) Save() error {
	if c == nil || c.path == "" {
		return nil
	}
	c.mu.Lock()
	data, err := json.MarshalIndent(c.fresh, "", "  ")
	c.mu.Unlock()
	if err != nil {
		return err
	}
	return atomicio.WriteFile(c.path, data, 0o644)
}
