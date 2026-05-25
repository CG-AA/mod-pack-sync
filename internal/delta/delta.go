// Package delta builds and applies delta packages (zip archives) that carry
// only the files which differ from a fresh-install baseline.
package delta

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cg-aa/mod-pack-sync/internal/atomicio"
	"github.com/cg-aa/mod-pack-sync/internal/manifest"
	"github.com/cg-aa/mod-pack-sync/internal/scan"
)

const (
	manifestName = "delta-manifest.json"
	filePrefix   = "files/"
	backupRoot   = ".modpack-sync/backups"
)

// Compute diffs the working entries against the baseline and returns the delta
// manifest. A file is written if it is new or its hash differs; a baseline file
// is deleted (carrying its expected pristine hash) if it is absent from the
// working set and not excluded.
func Compute(pack, version string, working []manifest.FileEntry, base *manifest.Baseline, ex *scan.Excluder) manifest.Delta {
	baseMap := base.FileMap()
	workMap := make(map[string]manifest.FileEntry, len(working))

	d := manifest.Delta{Pack: pack, Version: version, Created: time.Now().UTC()}
	for _, w := range working {
		workMap[w.Path] = w
		b, ok := baseMap[w.Path]
		if !ok || b.Hash != w.Hash {
			d.Write = append(d.Write, w)
		}
	}
	for p, b := range baseMap {
		if _, ok := workMap[p]; !ok && !ex.Match(p) {
			d.Delete = append(d.Delete, manifest.DeleteEntry{Path: p, Hash: b.Hash})
		}
	}
	sort.Slice(d.Write, func(i, j int) bool { return d.Write[i].Path < d.Write[j].Path })
	sort.Slice(d.Delete, func(i, j int) bool { return d.Delete[i].Path < d.Delete[j].Path })
	return d
}

// Pack writes a delta zip to outPath, reading file contents from workingRoot.
func Pack(outPath, workingRoot string, d manifest.Delta) error {
	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	if err := writeManifest(zw, d); err != nil {
		return err
	}
	for _, e := range d.Write {
		w, err := zw.Create(filePrefix + e.Path)
		if err != nil {
			return err
		}
		src, err := os.Open(filepath.Join(workingRoot, filepath.FromSlash(e.Path)))
		if err != nil {
			return err
		}
		_, err = io.Copy(w, src)
		src.Close()
		if err != nil {
			return err
		}
	}
	return zw.Close()
}

func writeManifest(zw *zip.Writer, d manifest.Delta) error {
	mw, err := zw.Create(manifestName)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(mw)
	enc.SetIndent("", "  ")
	return enc.Encode(d)
}

// ReadManifest opens a delta zip and returns its embedded manifest.
func ReadManifest(zipPath string) (*manifest.Delta, error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	for _, zf := range zr.File {
		if zf.Name == manifestName {
			rc, err := zf.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			var d manifest.Delta
			if err := json.NewDecoder(rc).Decode(&d); err != nil {
				return nil, err
			}
			return &d, nil
		}
	}
	return nil, fmt.Errorf("%s not found in package", manifestName)
}

// ApplyResult reports what Apply changed.
type ApplyResult struct {
	Written   int
	Skipped   int // already identical on disk
	Deleted   int
	Kept      int // delete skipped because the file no longer matches the baseline
	BackupDir string
}

// Apply extracts the delta onto targetRoot. Files that would be overwritten or
// deleted are first copied into a timestamped backup directory (alongside a copy
// of the applied manifest) so the operation can be reversed with Rollback.
// Deletes are guarded: a file is only removed if its current hash still matches
// the expected pristine hash. Paths are validated to stay inside targetRoot.
func Apply(zipPath, targetRoot string) (*ApplyResult, error) {
	d, err := ReadManifest(zipPath)
	if err != nil {
		return nil, err
	}
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, err
	}
	defer zr.Close()

	backupDir := filepath.Join(targetRoot, filepath.FromSlash(backupRoot), time.Now().Format("20060102-150405"))
	res := &ApplyResult{BackupDir: backupDir}

	// Write the rollback manifest before the first mutation, so a crash mid-apply
	// still leaves a recoverable backup (Rollback needs the manifest). It is
	// written lazily on the first real change, so a no-op apply leaves no backup.
	manifestSaved := false
	ensureManifest := func() error {
		if manifestSaved {
			return nil
		}
		if err := saveManifest(d, filepath.Join(backupDir, manifestName)); err != nil {
			return err
		}
		manifestSaved = true
		return nil
	}

	files := make(map[string]*zip.File, len(zr.File))
	for _, zf := range zr.File {
		files[zf.Name] = zf
	}

	for _, e := range d.Write {
		dst, err := safeJoin(targetRoot, e.Path)
		if err != nil {
			return res, err
		}
		if cur, _, herr := scan.HashFile(dst); herr == nil && cur == e.Hash {
			res.Skipped++
			continue
		}
		if err := ensureManifest(); err != nil {
			return res, err
		}
		if err := backup(dst, e.Path, backupDir); err != nil {
			return res, err
		}
		zf, ok := files[filePrefix+e.Path]
		if !ok {
			return res, fmt.Errorf("package missing content for %s", e.Path)
		}
		if err := extractOne(zf, dst, e.Hash); err != nil {
			return res, err
		}
		res.Written++
	}

	for _, e := range d.Delete {
		dst, err := safeJoin(targetRoot, e.Path)
		if err != nil {
			return res, err
		}
		cur, _, herr := scan.HashFile(dst)
		if herr != nil {
			continue // already absent
		}
		if cur != e.Hash {
			res.Kept++ // receiver changed this base file; leave it alone
			continue
		}
		if err := ensureManifest(); err != nil {
			return res, err
		}
		if err := backup(dst, e.Path, backupDir); err != nil {
			return res, err
		}
		if err := os.Remove(dst); err != nil {
			return res, err
		}
		res.Deleted++
	}

	return res, nil
}

// Rollback restores the most recent backup under targetRoot: files that were
// overwritten or deleted are copied back, and files the apply newly created
// (present in the manifest's Write list but absent from the backup) are removed.
func Rollback(targetRoot string) (string, error) {
	root := filepath.Join(targetRoot, filepath.FromSlash(backupRoot))
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", fmt.Errorf("no backups found: %w", err)
	}
	var stamps []string
	for _, e := range entries {
		if e.IsDir() {
			stamps = append(stamps, e.Name())
		}
	}
	if len(stamps) == 0 {
		return "", fmt.Errorf("no backups found in %s", root)
	}
	sort.Strings(stamps)
	backupDir := filepath.Join(root, stamps[len(stamps)-1])

	mpath := filepath.Join(backupDir, manifestName)
	data, err := os.ReadFile(mpath)
	if err != nil {
		return backupDir, fmt.Errorf("backup is missing its manifest: %w", err)
	}
	var d manifest.Delta
	if err := json.Unmarshal(data, &d); err != nil {
		return backupDir, err
	}

	for _, e := range d.Write {
		dst, err := safeJoin(targetRoot, e.Path)
		if err != nil {
			return backupDir, err
		}
		src := filepath.Join(backupDir, filepath.FromSlash(e.Path))
		if _, serr := os.Stat(src); serr == nil {
			if err := copyFile(src, dst); err != nil {
				return backupDir, err
			}
		} else if cur, _, herr := scan.HashFile(dst); herr == nil && cur == e.Hash {
			// No backup and the file holds exactly what the apply wrote: it was
			// newly created, so remove it. A file whose hash differs (e.g. an
			// untouched original of an entry a crashed apply never reached) is
			// left alone.
			os.Remove(dst)
		}
	}
	for _, e := range d.Delete {
		src := filepath.Join(backupDir, filepath.FromSlash(e.Path))
		if _, serr := os.Stat(src); serr != nil {
			continue
		}
		dst, err := safeJoin(targetRoot, e.Path)
		if err != nil {
			return backupDir, err
		}
		if err := copyFile(src, dst); err != nil {
			return backupDir, err
		}
	}
	return backupDir, nil
}

func safeJoin(root, rel string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(rel))
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe path in package: %q", rel)
	}
	return filepath.Join(root, clean), nil
}

func extractOne(zf *zip.File, dst, wantHash string) error {
	rc, err := zf.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	return atomicio.WriteReaderVerify(dst, rc, 0o644, wantHash)
}

// backup preserves the current file at src into backupDir before it is
// overwritten or removed. It hardlinks the file (instant, no data copy — a big
// win for thousands of small files), falling back to an atomic copy when linking
// is unsupported (cross-device, FAT/exFAT, Windows). Because the live file is
// later replaced via atomic rename (a new inode), the hardlinked backup keeps
// pointing at the original content.
func backup(src, rel, backupDir string) error {
	if _, err := os.Stat(src); err != nil {
		return nil // nothing to back up (new file)
	}
	dst := filepath.Join(backupDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.Link(src, dst); err == nil {
		return nil
	}
	return copyFile(src, dst)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	return atomicio.WriteReader(dst, in, 0o644)
}

func saveManifest(d *manifest.Delta, path string) error {
	data, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	return atomicio.WriteFile(path, data, 0o644)
}
