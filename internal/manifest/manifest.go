// Package manifest defines the on-disk formats for baseline snapshots and
// delta packages, and helpers to read/write them.
package manifest

import (
	"encoding/json"
	"os"
	"time"
)

// FileEntry describes a single file by its instance-relative path (always
// forward-slash separated), size and content hash.
type FileEntry struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
	Hash string `json:"hash"` // sha256, lowercase hex
}

// Baseline is a snapshot of a freshly installed pack. It is generated once per
// pack version and committed to the repo so the sender can diff against it.
type Baseline struct {
	Pack    string      `json:"pack"`
	Version string      `json:"version"`
	Created time.Time   `json:"created"`
	Files   []FileEntry `json:"files"`
}

// FileMap indexes the baseline files by path for quick lookup.
func (b *Baseline) FileMap() map[string]FileEntry {
	m := make(map[string]FileEntry, len(b.Files))
	for _, f := range b.Files {
		m[f.Path] = f
	}
	return m
}

// DeleteEntry names a base-pack file the sender removed. Hash is the file's
// expected pristine (fresh-install) hash; the receiver only removes the file if
// its on-disk content still matches, so a stale baseline can never cause a
// destructive delete of a file the receiver has changed.
type DeleteEntry struct {
	Path string `json:"path"`
	Hash string `json:"hash"`
}

// Delta is the manifest embedded inside a delta package. Write lists files to
// create/overwrite on the receiver; Delete lists base-pack files the sender
// removed and that the receiver should remove too.
type Delta struct {
	Pack    string        `json:"pack"`
	Version string        `json:"version"`
	Created time.Time     `json:"created"`
	Write   []FileEntry   `json:"write"`
	Delete  []DeleteEntry `json:"delete"`
}

// LoadBaseline reads a baseline manifest from a JSON file.
func LoadBaseline(path string) (*Baseline, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var b Baseline
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, err
	}
	return &b, nil
}

// Save writes the baseline manifest as indented JSON.
func (b *Baseline) Save(path string) error {
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
