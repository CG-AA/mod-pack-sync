// Package scan walks an instance directory, applying an exclude list, and
// produces hashed file entries.
package scan

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cg-aa/mod-pack-sync/internal/manifest"
)

// DefaultExcludes are paths that should never be synced: world saves, logs,
// caches, per-user preferences, and the sync tool's own files. Entries ending
// in "/" match a directory and everything under it; entries containing "*" are
// matched as globs against both the full relative path and the base name.
var DefaultExcludes = []string{
	// tool state and binaries (the tool lives in the instance root)
	".modpack-sync/",
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
	var entries []manifest.FileEntry
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
		h, size, herr := HashFile(p)
		if herr != nil {
			return herr
		}
		entries = append(entries, manifest.FileEntry{Path: rel, Size: size, Hash: h})
		return nil
	})
	if err != nil {
		return nil, err
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
