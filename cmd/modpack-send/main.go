// Command modpack-send builds a delta of the local mod-pack against a
// fresh-install baseline and sends it via magic-wormhole (or to a file).
//
// Usage:
//
//	modpack-send                 send the current pack's customizations
//	modpack-send --out delta.zip save the delta to a file instead of sending
//	modpack-send capture-baseline   snapshot a FRESH install into baseline.json
package main

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"time"

	"github.com/cg-aa/mod-pack-sync/internal/atomicio"
	"github.com/cg-aa/mod-pack-sync/internal/delta"
	"github.com/cg-aa/mod-pack-sync/internal/i18n"
	"github.com/cg-aa/mod-pack-sync/internal/logx"
	"github.com/cg-aa/mod-pack-sync/internal/manifest"
	"github.com/cg-aa/mod-pack-sync/internal/root"
	"github.com/cg-aa/mod-pack-sync/internal/scan"
	"github.com/cg-aa/mod-pack-sync/internal/transport"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "capture-baseline" {
		captureBaseline(os.Args[2:])
		return
	}
	send(os.Args[1:])
}

type common struct {
	rootDir  string
	settings root.Settings
	log      *logx.Logger
}

func setup(rootFlag, langFlag string) common {
	rootDir, err := root.Resolve(rootFlag)
	if err != nil {
		panic(err)
	}
	settings, _ := root.LoadSettings(rootDir)
	lang := langFlag
	if lang == "" {
		lang = settings.Lang
	}
	t := i18n.New(i18n.Detect(lang))
	log := logx.New(root.StateDir(rootDir), t)
	return common{rootDir: rootDir, settings: settings, log: log}
}

func excluder(s root.Settings) *scan.Excluder {
	pats := append([]string{}, scan.DefaultExcludes...)
	pats = append(pats, scan.SelfExcludes()...)
	pats = append(pats, s.Excludes...)
	return scan.NewExcluder(pats)
}

func label(s root.Settings, rootDir, override string) string {
	if override != "" {
		return override
	}
	if s.Label != "" {
		return s.Label
	}
	return filepath.Base(rootDir)
}

// loadCache returns the scan hash cache to use, honoring --rehash and --no-cache.
func loadCache(rootDir string, rehash, noCache bool) *scan.HashCache {
	if noCache {
		return nil
	}
	p := filepath.Join(root.StateDir(rootDir), "hashcache.json")
	if rehash {
		return scan.NewHashCache(p)
	}
	return scan.LoadHashCache(p)
}

func send(args []string) {
	fs := flag.NewFlagSet("modpack-send", flag.ExitOnError)
	rootFlag := fs.String("root", "", "instance root (default: the program's own folder)")
	langFlag := fs.String("lang", "", "language: en or zh-TW (default: auto-detect)")
	out := fs.String("out", "", "write the delta to this file instead of sending over wormhole")
	relayFlag := fs.String("relay", "", "custom wormhole rendezvous URL")
	durable := fs.Bool("durable", false, "fsync writes for power-loss durability (slower on many small files)")
	rehash := fs.Bool("rehash", false, "ignore the scan hash cache and re-hash every file")
	noCache := fs.Bool("no-cache", false, "do not read or write the scan hash cache")
	retriesFlag := fs.Int("retries", 0, "transfer attempts before giving up (default 3)")
	retryTimeout := fs.Duration("retry-timeout", time.Hour, "per-attempt transfer timeout")
	fs.Parse(args)

	c := setup(*rootFlag, *langFlag)
	defer c.log.Close()
	atomicio.Fsync = *durable || c.settings.Durable
	c.log.Say("welcome_send")

	blPath := root.BaselinePath(c.rootDir)
	base, err := manifest.LoadBaseline(blPath)
	if err != nil {
		c.log.Say("baseline_missing", blPath)
		c.log.Fatal(err)
	}

	c.log.Say("scanning")
	ex := excluder(c.settings)
	cache := loadCache(c.rootDir, *rehash, *noCache)
	working, err := scan.WalkCached(c.rootDir, ex, cache)
	if err != nil {
		c.log.Fatal(err)
	}
	if cache != nil {
		_ = cache.Save()
	}

	d := delta.Compute(base.Pack, base.Version, working, base, ex)
	if len(d.Write) == 0 && len(d.Delete) == 0 {
		c.log.Say("nothing_to_send")
		c.log.PauseIfWindows()
		return
	}
	c.log.Say("delta_summary", len(d.Write), len(d.Delete), base.Pack)

	pkg := *out
	if pkg == "" {
		pkg = filepath.Join(root.StateDir(c.rootDir), "outgoing-delta.zip")
	}
	if err := delta.Pack(pkg, c.rootDir, d); err != nil {
		c.log.Fatal(err)
	}

	if *out != "" {
		c.log.Say("saved_file", pkg)
		c.log.PauseIfWindows()
		return
	}

	relay := *relayFlag
	if relay == "" {
		relay = c.settings.Relay
	}
	retries := resolveRetries(*retriesFlag, c.settings.Retries)
	c.log.Say("sending")
	var sendErr error
	for attempt := 1; attempt <= retries; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), *retryTimeout)
		sendErr = transport.SendFile(ctx, relay, pkg,
			func(code string) { c.log.Raw(code); c.log.Say("send_waiting") },
			c.log.Progress)
		cancel()
		if sendErr == nil {
			break
		}
		if attempt < retries {
			c.log.Say("send_retry", attempt, retries)
			backoff(attempt)
		}
	}
	if sendErr != nil {
		// Keep the prepared package so the user can retry without re-scanning.
		c.log.Say("send_failed_kept", pkg)
		c.log.Fatal(sendErr)
	}
	os.Remove(pkg)
	c.log.Say("send_done")
	c.log.PauseIfWindows()
}

// resolveRetries picks the attempt count: the flag if set, else the setting,
// else a default of 3.
func resolveRetries(flag, setting int) int {
	if flag > 0 {
		return flag
	}
	if setting > 0 {
		return setting
	}
	return 3
}

// backoff sleeps for a capped exponential delay before retry attempt+1.
func backoff(attempt int) {
	d := time.Duration(1<<attempt) * time.Second // 2s, 4s, 8s, ...
	if d > 16*time.Second {
		d = 16 * time.Second
	}
	time.Sleep(d)
}

func captureBaseline(args []string) {
	fs := flag.NewFlagSet("capture-baseline", flag.ExitOnError)
	rootFlag := fs.String("root", "", "instance root (default: the program's own folder)")
	langFlag := fs.String("lang", "", "language: en or zh-TW (default: auto-detect)")
	labelFlag := fs.String("label", "", "human label for this pack (default: folder name)")
	versionFlag := fs.String("version", "", "pack version label")
	durable := fs.Bool("durable", false, "fsync writes for power-loss durability (slower on many small files)")
	rehash := fs.Bool("rehash", false, "ignore the scan hash cache and re-hash every file")
	noCache := fs.Bool("no-cache", false, "do not read or write the scan hash cache")
	fs.Parse(args)

	c := setup(*rootFlag, *langFlag)
	defer c.log.Close()
	atomicio.Fsync = *durable || c.settings.Durable

	c.log.Say("capturing_baseline")
	ex := excluder(c.settings)
	cache := loadCache(c.rootDir, *rehash, *noCache)
	files, err := scan.WalkCached(c.rootDir, ex, cache)
	if err != nil {
		c.log.Fatal(err)
	}
	if cache != nil {
		_ = cache.Save()
	}
	b := &manifest.Baseline{
		Pack:    label(c.settings, c.rootDir, *labelFlag),
		Version: *versionFlag,
		Created: time.Now().UTC(),
		Files:   files,
	}
	if err := os.MkdirAll(root.StateDir(c.rootDir), 0o755); err != nil {
		c.log.Fatal(err)
	}
	blPath := root.BaselinePath(c.rootDir)
	if err := b.Save(blPath); err != nil {
		c.log.Fatal(err)
	}
	c.log.Say("baseline_saved", blPath, len(files))
	c.log.PauseIfWindows()
}
