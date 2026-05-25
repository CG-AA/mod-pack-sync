package scan

import "testing"

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
