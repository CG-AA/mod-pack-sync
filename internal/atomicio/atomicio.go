// Package atomicio writes files atomically: data is streamed into a temp file in
// the destination's own directory, then renamed over the target. os.Rename gives
// an atomic replace on both POSIX and Windows (MoveFileEx), so a reader always
// sees either the complete old file or the complete new one — never a torn write —
// even if the process is killed mid-copy.
//
// It lives in its own leaf package (stdlib only) so manifest, scan, and delta can
// all use it without an import cycle.
package atomicio

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
)

// TempPrefix marks the temp files this package creates, so callers can exclude
// any left behind by a crash.
const TempPrefix = ".modpack-sync-tmp-"

// Fsync, when true, makes writes durable across power loss by syncing the file's
// data and its parent directory around the rename. It is off by default because a
// per-file fsync is very slow on packs with thousands of small files, and plain
// temp+rename already prevents torn files against the common failures (process
// kill, Ctrl-C, dropped transfer). Set once at startup from a --durable flag.
var Fsync bool

// WriteReader atomically writes all of r to path, creating parent dirs as needed.
func WriteReader(path string, r io.Reader, perm os.FileMode) error {
	return write(path, r, perm, "")
}

// WriteReaderVerify is WriteReader plus an integrity check: the stream is hashed
// while copying and, if the lowercase-hex sha256 does not equal wantHash, the temp
// file is removed and an error returned — path is left untouched.
func WriteReaderVerify(path string, r io.Reader, perm os.FileMode, wantHash string) error {
	return write(path, r, perm, wantHash)
}

// WriteFile atomically writes data to path.
func WriteFile(path string, data []byte, perm os.FileMode) error {
	return write(path, bytes.NewReader(data), perm, "")
}

func write(path string, r io.Reader, perm os.FileMode, wantHash string) (err error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, TempPrefix+"*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			tmp.Close()
			os.Remove(tmpName)
		}
	}()

	var w io.Writer = tmp
	var h hash.Hash
	if wantHash != "" {
		h = sha256.New()
		w = io.MultiWriter(tmp, h)
	}
	if _, err := io.Copy(w, r); err != nil {
		return err
	}
	if h != nil {
		if got := hex.EncodeToString(h.Sum(nil)); got != wantHash {
			return fmt.Errorf("content hash mismatch for %s: got %s, want %s", path, got, wantHash)
		}
	}
	if Fsync {
		if err := tmp.Sync(); err != nil {
			return err
		}
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	committed = true
	if Fsync {
		if d, derr := os.Open(dir); derr == nil {
			d.Sync()
			d.Close()
		}
	}
	return nil
}
