// Package root resolves the mod-pack instance directory (the folder the tool was
// dropped into) and loads optional per-machine settings.
package root

import (
	"encoding/json"
	"os"
	"path/filepath"
)

const (
	stateDir     = ".modpack-sync"
	settingsName = "settings.json"
	baselineName = "baseline.json"
)

// Settings are optional overrides read from <root>/.modpack-sync/settings.json.
// Zero config is fine; this exists mainly for the double-click user who cannot
// pass command-line flags.
type Settings struct {
	Lang     string   `json:"lang"`     // "en" or "zh-TW"
	Label    string   `json:"label"`    // human label for the pack
	Relay    string   `json:"relay"`    // custom wormhole rendezvous URL
	Excludes []string `json:"excludes"` // extra exclude patterns
	Durable  bool     `json:"durable"`  // fsync writes for power-loss durability
	Retries  int      `json:"retries"`  // transfer attempts before giving up (default 3)
}

// Resolve returns the instance root. If override is non-empty it is used as-is;
// otherwise the directory containing the running executable is used, so a
// double-clicked .exe operates on its own folder regardless of working dir.
func Resolve(override string) (string, error) {
	if override != "" {
		return filepath.Abs(override)
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return "", err
	}
	return filepath.Dir(exe), nil
}

// StateDir is <root>/.modpack-sync.
func StateDir(root string) string { return filepath.Join(root, stateDir) }

// BaselinePath is <root>/.modpack-sync/baseline.json.
func BaselinePath(root string) string { return filepath.Join(StateDir(root), baselineName) }

// LoadSettings reads optional settings; a missing file yields zero-value
// Settings and no error.
func LoadSettings(root string) (Settings, error) {
	var s Settings
	data, err := os.ReadFile(filepath.Join(StateDir(root), settingsName))
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return s, err
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return s, err
	}
	return s, nil
}
