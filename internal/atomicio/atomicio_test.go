package atomicio

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sum(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func noTempLeak(t *testing.T, dir string) {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), TempPrefix) {
			t.Errorf("temp file leaked: %s", e.Name())
		}
	}
}

func TestWriteReaderAtomic(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sub", "f.txt")
	if err := WriteReader(p, strings.NewReader("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p)
	if string(got) != "hello" {
		t.Errorf("content = %q", got)
	}
	noTempLeak(t, filepath.Dir(p))
}

func TestWriteReaderVerifyMismatchLeavesTargetUntouched(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(p, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := WriteReaderVerify(p, strings.NewReader("corrupt"), 0o644, sum("expected"))
	if err == nil {
		t.Fatal("expected hash-mismatch error, got nil")
	}
	got, _ := os.ReadFile(p)
	if string(got) != "original" {
		t.Errorf("target was modified on mismatch: %q", got)
	}
	noTempLeak(t, dir)
}

func TestWriteReaderVerifyMatch(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	if err := WriteReaderVerify(p, strings.NewReader("payload"), 0o644, sum("payload")); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p)
	if string(got) != "payload" {
		t.Errorf("content = %q", got)
	}
}

type partialReader struct {
	data []byte
	off  int
}

func (r *partialReader) Read(p []byte) (int, error) {
	if r.off >= len(r.data) {
		return 0, errors.New("simulated mid-stream failure")
	}
	n := copy(p, r.data[r.off:r.off+1]) // dribble one byte then fail next call
	r.off += n
	return n, nil
}

func TestWriteReaderErrorLeavesTargetUntouched(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(p, []byte("keep-me"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := WriteReader(p, &partialReader{data: []byte("xyz")}, 0o644)
	if err == nil {
		t.Fatal("expected error from failing reader")
	}
	got, _ := os.ReadFile(p)
	if string(got) != "keep-me" {
		t.Errorf("target was modified on write failure: %q", got)
	}
	noTempLeak(t, dir)
}

var _ io.Reader = (*partialReader)(nil)
