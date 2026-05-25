package scan

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestExcluder(t *testing.T) {
	ex := NewExcluder([]string{
		".modpack-sync/", "saves/", "*.log", "options.txt", "modpack-send*",
	})
	cases := map[string]bool{
		"mods/jei.jar":                false,
		"config/foo.toml":             false,
		"saves/world/level.dat":       true,
		"saves":                       true,
		"latest.log":                  true,
		"logs/debug.log":              true,
		"options.txt":                 true,
		".modpack-sync/baseline.json": true,
		"modpack-send.exe":            true,
		"kubejs/server_scripts/a.js":  false,
	}
	for path, want := range cases {
		if got := ex.Match(path); got != want {
			t.Errorf("Match(%q) = %v, want %v", path, got, want)
		}
	}
}

func hashOf(t *testing.T, root, rel string) string {
	t.Helper()
	h, _, err := HashFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestHashCacheReuseAndInvalidate(t *testing.T) {
	root := t.TempDir()
	f := filepath.Join(root, "a.txt")
	if err := os.WriteFile(f, []byte("AAAA"), 0o644); err != nil {
		t.Fatal(err)
	}
	fixed := time.Unix(1_600_000_000, 0)
	if err := os.Chtimes(f, fixed, fixed); err != nil {
		t.Fatal(err)
	}
	hashA := hashOf(t, root, "a.txt")

	ex := NewExcluder(nil)
	cachePath := filepath.Join(t.TempDir(), "hashcache.json")

	cache := LoadHashCache(cachePath)
	if _, err := WalkCached(root, ex, cache); err != nil {
		t.Fatal(err)
	}
	if err := cache.Save(); err != nil {
		t.Fatal(err)
	}

	// Change the content but keep size and mtime identical: a size+mtime-keyed
	// cache must reuse the stale hash, proving it did not re-read the file.
	if err := os.WriteFile(f, []byte("BBBB"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(f, fixed, fixed); err != nil {
		t.Fatal(err)
	}
	reused, err := WalkCached(root, ex, LoadHashCache(cachePath))
	if err != nil {
		t.Fatal(err)
	}
	if len(reused) != 1 || reused[0].Hash != hashA {
		t.Fatalf("expected cached (stale) hash %s, got %+v", hashA, reused)
	}

	// Now bump the mtime: the cache entry is invalidated and the file re-hashed.
	bumped := fixed.Add(time.Hour)
	if err := os.Chtimes(f, bumped, bumped); err != nil {
		t.Fatal(err)
	}
	hashB := hashOf(t, root, "a.txt")
	fresh, err := WalkCached(root, ex, LoadHashCache(cachePath))
	if err != nil {
		t.Fatal(err)
	}
	if len(fresh) != 1 || fresh[0].Hash != hashB {
		t.Fatalf("expected re-hashed value %s, got %+v", hashB, fresh)
	}
}
