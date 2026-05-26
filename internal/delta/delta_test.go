package delta

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cg-aa/mod-pack-sync/internal/atomicio"
	"github.com/cg-aa/mod-pack-sync/internal/manifest"
	"github.com/cg-aa/mod-pack-sync/internal/scan"
)

func sumStr(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func buildRawZip(t *testing.T, path string, d manifest.Delta, contents map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	mw, err := zw.Create(manifestName)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(mw).Encode(d); err != nil {
		t.Fatal(err)
	}
	for rel, c := range contents {
		w, err := zw.Create(filePrefix + rel)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(c)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
}

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

func TestExtractVerifyRejectsCorruptContent(t *testing.T) {
	// A package whose stored bytes do not match the manifest hash must be rejected
	// before the live file is touched.
	dir := t.TempDir()
	pkg := filepath.Join(dir, "bad.zip")
	d := manifest.Delta{
		Pack:  "test",
		Write: []manifest.FileEntry{{Path: "mods/x.jar", Size: 4, Hash: sumStr("good")}},
	}
	buildRawZip(t, pkg, d, map[string]string{"mods/x.jar": "BAD!"})

	recv := t.TempDir()
	write(t, recv, "mods/x.jar", "orig")

	if _, err := Apply(pkg, recv); err == nil {
		t.Fatal("expected apply to fail on content hash mismatch")
	}
	if c, _ := read(t, recv, "mods/x.jar"); c != "orig" {
		t.Errorf("corrupt content must not replace the live file, got %q", c)
	}
}

func TestRollbackHashGuard(t *testing.T) {
	// Reproduce the on-disk state a crash mid-apply would leave and confirm
	// Rollback restores completed changes without destroying an untouched original.
	target := t.TempDir()
	write(t, target, "over.txt", "new-content")           // overwrite that completed
	write(t, target, "created.txt", "created-content")    // new file that completed
	write(t, target, "untouched.txt", "original-content") // overwrite not yet reached

	d := &manifest.Delta{
		Pack: "test",
		Write: []manifest.FileEntry{
			{Path: "over.txt", Hash: sumStr("new-content")},
			{Path: "created.txt", Hash: sumStr("created-content")},
			{Path: "untouched.txt", Hash: sumStr("would-be-applied")}, // != on-disk
		},
	}
	backupDir := filepath.Join(target, ".modpack-sync", "backups", "20990101-000000")
	if err := saveManifest(d, filepath.Join(backupDir, manifestName)); err != nil {
		t.Fatal(err)
	}
	write(t, backupDir, "over.txt", "old-content") // pre-apply backup of the overwrite

	if _, err := Rollback(target); err != nil {
		t.Fatal(err)
	}
	if c, _ := read(t, target, "over.txt"); c != "old-content" {
		t.Errorf("over.txt should be restored from backup, got %q", c)
	}
	if _, ok := read(t, target, "created.txt"); ok {
		t.Error("created.txt (newly created, matches applied hash) should be removed")
	}
	if c, _ := read(t, target, "untouched.txt"); c != "original-content" {
		t.Errorf("untouched original must be preserved, got %q", c)
	}
}

func TestPackCompressionMethod(t *testing.T) {
	working := t.TempDir()
	write(t, working, "mods/a.jar", strings.Repeat("payload", 500))   // already-compressed type
	write(t, working, "config/b.toml", strings.Repeat("hello ", 500)) // compressible text
	entries, err := scan.Walk(working, scan.NewExcluder(nil))
	if err != nil {
		t.Fatal(err)
	}
	d := manifest.Delta{Pack: "t", Write: entries}
	pkg := filepath.Join(t.TempDir(), "d.zip")
	if err := Pack(pkg, working, d); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.OpenReader(pkg)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	methods := map[string]uint16{}
	for _, f := range zr.File {
		methods[f.Name] = f.Method
	}
	if m := methods[filePrefix+"mods/a.jar"]; m != zip.Store {
		t.Errorf("jar method = %d, want Store (%d)", m, zip.Store)
	}
	if m := methods[filePrefix+"config/b.toml"]; m != zip.Deflate {
		t.Errorf("toml method = %d, want Deflate (%d)", m, zip.Deflate)
	}
}

func TestBackupHardlink(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "live.txt")
	if err := os.WriteFile(src, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	backupDir := filepath.Join(dir, "bk")
	if err := backup(src, "live.txt", backupDir); err != nil {
		t.Fatal(err)
	}
	bk := filepath.Join(backupDir, "live.txt")
	si, _ := os.Stat(src)
	bi, err := os.Stat(bk)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(si, bi) {
		t.Skip("filesystem does not support hardlinks; copy fallback path")
	}
	// Atomic replace of the live file must leave the hardlinked backup untouched.
	if err := atomicio.WriteReader(src, strings.NewReader("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if c, _ := os.ReadFile(bk); string(c) != "old" {
		t.Errorf("backup content changed after atomic overwrite: %q", c)
	}
	if c, _ := os.ReadFile(src); string(c) != "new" {
		t.Errorf("live file = %q, want new", c)
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
