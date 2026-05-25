package delta

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cg-aa/mod-pack-sync/internal/manifest"
	"github.com/cg-aa/mod-pack-sync/internal/scan"
)

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, root, rel string) (string, bool) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return "", false
	}
	return string(b), true
}

func snapshot(t *testing.T, root string, ex *scan.Excluder) *manifest.Baseline {
	t.Helper()
	files, err := scan.Walk(root, ex)
	if err != nil {
		t.Fatal(err)
	}
	return &manifest.Baseline{Pack: "test", Files: files}
}

func TestRoundTripAndRollback(t *testing.T) {
	ex := scan.NewExcluder(scan.DefaultExcludes)

	// fresh install
	fresh := t.TempDir()
	write(t, fresh, "mods/base.jar", "base-v1")
	write(t, fresh, "mods/removeme.jar", "doomed")
	write(t, fresh, "config/keep.toml", "k")

	base := snapshot(t, fresh, ex)

	// working = fresh + customizations: change base.jar, add a mod, delete removeme
	working := t.TempDir()
	write(t, working, "mods/base.jar", "base-v2-modified")
	write(t, working, "config/keep.toml", "k")
	write(t, working, "mods/extra.jar", "added")
	write(t, working, "kubejs/script.js", "custom")

	workEntries, err := scan.Walk(working, ex)
	if err != nil {
		t.Fatal(err)
	}
	d := Compute("test", "", workEntries, base, ex)

	pkg := filepath.Join(t.TempDir(), "delta.zip")
	if err := Pack(pkg, working, d); err != nil {
		t.Fatal(err)
	}

	// receiver = a copy of fresh
	recv := t.TempDir()
	write(t, recv, "mods/base.jar", "base-v1")
	write(t, recv, "mods/removeme.jar", "doomed")
	write(t, recv, "config/keep.toml", "k")

	res, err := Apply(pkg, recv)
	if err != nil {
		t.Fatal(err)
	}

	// receiver should now match working (minus excludes)
	if c, _ := read(t, recv, "mods/base.jar"); c != "base-v2-modified" {
		t.Errorf("base.jar = %q", c)
	}
	if c, _ := read(t, recv, "mods/extra.jar"); c != "added" {
		t.Errorf("extra.jar = %q", c)
	}
	if c, _ := read(t, recv, "kubejs/script.js"); c != "custom" {
		t.Errorf("script.js = %q", c)
	}
	if _, ok := read(t, recv, "mods/removeme.jar"); ok {
		t.Error("removeme.jar should have been deleted")
	}
	if res.Deleted != 1 {
		t.Errorf("Deleted = %d, want 1", res.Deleted)
	}

	// rollback should restore the original fresh state exactly
	if _, err := Rollback(recv); err != nil {
		t.Fatal(err)
	}
	if c, _ := read(t, recv, "mods/base.jar"); c != "base-v1" {
		t.Errorf("after rollback base.jar = %q, want base-v1", c)
	}
	if c, ok := read(t, recv, "mods/removeme.jar"); !ok || c != "doomed" {
		t.Errorf("after rollback removeme.jar = %q/%v, want restored", c, ok)
	}
	if _, ok := read(t, recv, "mods/extra.jar"); ok {
		t.Error("after rollback extra.jar (newly created) should be gone")
	}
}

func TestGuardedDeleteKeepsModifiedBaseFile(t *testing.T) {
	ex := scan.NewExcluder(scan.DefaultExcludes)

	fresh := t.TempDir()
	write(t, fresh, "mods/gone.jar", "pristine")
	base := snapshot(t, fresh, ex)

	// sender removed gone.jar
	working := t.TempDir()
	write(t, working, "mods/other.jar", "x")
	workEntries, _ := scan.Walk(working, ex)
	d := Compute("test", "", workEntries, base, ex)

	pkg := filepath.Join(t.TempDir(), "delta.zip")
	if err := Pack(pkg, working, d); err != nil {
		t.Fatal(err)
	}

	// receiver MODIFIED gone.jar -> delete must be skipped (hash mismatch)
	recv := t.TempDir()
	write(t, recv, "mods/gone.jar", "i-changed-this")

	res, err := Apply(pkg, recv)
	if err != nil {
		t.Fatal(err)
	}
	if res.Deleted != 0 || res.Kept != 1 {
		t.Errorf("Deleted=%d Kept=%d, want 0/1", res.Deleted, res.Kept)
	}
	if c, ok := read(t, recv, "mods/gone.jar"); !ok || c != "i-changed-this" {
		t.Errorf("modified base file should be kept, got %q/%v", c, ok)
	}
}

func TestSkipIdenticalWrite(t *testing.T) {
	ex := scan.NewExcluder(scan.DefaultExcludes)
	fresh := t.TempDir()
	write(t, fresh, "config/a.toml", "v1")
	base := snapshot(t, fresh, ex)

	working := t.TempDir()
	write(t, working, "config/a.toml", "v2")
	workEntries, _ := scan.Walk(working, ex)
	d := Compute("test", "", workEntries, base, ex)
	pkg := filepath.Join(t.TempDir(), "delta.zip")
	if err := Pack(pkg, working, d); err != nil {
		t.Fatal(err)
	}

	// receiver already has the target content
	recv := t.TempDir()
	write(t, recv, "config/a.toml", "v2")
	res, err := Apply(pkg, recv)
	if err != nil {
		t.Fatal(err)
	}
	if res.Written != 0 || res.Skipped != 1 {
		t.Errorf("Written=%d Skipped=%d, want 0/1", res.Written, res.Skipped)
	}
}
