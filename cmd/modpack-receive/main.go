// Command modpack-receive downloads a delta via magic-wormhole (or reads one
// from a file) and applies it onto the local fresh-install mod-pack, backing up
// every changed file first.
//
// Usage:
//
//	modpack-receive                 receive over wormhole and apply
//	modpack-receive --in delta.zip  apply a delta from a file
//	modpack-receive rollback        undo the last apply from backup
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
	"github.com/cg-aa/mod-pack-sync/internal/root"
	"github.com/cg-aa/mod-pack-sync/internal/transport"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "rollback" {
		rollback(os.Args[2:])
		return
	}
	receive(os.Args[1:])
}

func setup(rootFlag, langFlag string) (string, root.Settings, *logx.Logger) {
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
	return rootDir, settings, logx.New(root.StateDir(rootDir), t)
}

func receive(args []string) {
	fs := flag.NewFlagSet("modpack-receive", flag.ExitOnError)
	rootFlag := fs.String("root", "", "instance root (default: the program's own folder)")
	langFlag := fs.String("lang", "", "language: en or zh-TW (default: auto-detect)")
	in := fs.String("in", "", "apply a delta from this file instead of wormhole")
	relayFlag := fs.String("relay", "", "custom wormhole rendezvous URL")
	durable := fs.Bool("durable", false, "fsync writes for power-loss durability (slower on many small files)")
	retriesFlag := fs.Int("retries", 0, "transfer attempts before giving up (default 3)")
	retryTimeout := fs.Duration("retry-timeout", time.Hour, "per-attempt transfer timeout")
	fs.Parse(args)

	rootDir, settings, log := setup(*rootFlag, *langFlag)
	defer log.Close()
	atomicio.Fsync = *durable || settings.Durable
	log.Say("welcome_receive")

	pkg := *in
	if pkg == "" {
		relay := *relayFlag
		if relay == "" {
			relay = settings.Relay
		}
		pkg = filepath.Join(root.StateDir(rootDir), "incoming-delta.zip")
		if err := os.MkdirAll(filepath.Dir(pkg), 0o755); err != nil {
			log.Fatal(err)
		}
		retries := transport.Attempts(*retriesFlag, settings.Retries)
		var rerr error
		for attempt := 1; attempt <= retries; attempt++ {
			// Each attempt needs a fresh code: a dropped wormhole transfer cannot
			// resume, so the sender generates a new code on its retry.
			code := log.Prompt("enter_code")
			if code == "" {
				log.Say("cancelled")
				log.PauseIfWindows()
				return
			}
			log.Say("receiving")
			ctx, cancel := context.WithTimeout(context.Background(), *retryTimeout)
			rerr = transport.Receive(ctx, relay, code, pkg, log.Progress)
			cancel()
			if rerr == nil {
				break
			}
			if attempt < retries {
				log.Say("receive_retry", attempt, retries)
				transport.Backoff(attempt)
			}
		}
		if rerr != nil {
			log.Fatal(rerr)
		}
		defer os.Remove(pkg)
	} else {
		log.Say("applying_file", pkg)
	}

	log.Say("applying")
	res, err := delta.Apply(pkg, rootDir)
	if err != nil {
		log.Fatal(err)
	}
	log.Say("apply_done", res.Written, res.Deleted, res.BackupDir)
	if res.Kept > 0 {
		log.Say("apply_kept", res.Kept)
	}
	log.PauseIfWindows()
}

func rollback(args []string) {
	fs := flag.NewFlagSet("rollback", flag.ExitOnError)
	rootFlag := fs.String("root", "", "instance root (default: the program's own folder)")
	langFlag := fs.String("lang", "", "language: en or zh-TW (default: auto-detect)")
	fs.Parse(args)

	rootDir, _, log := setup(*rootFlag, *langFlag)
	defer log.Close()

	dir, err := delta.Rollback(rootDir)
	if err != nil {
		log.Say("rollback_none", dir)
		log.Fatal(err)
	}
	log.Say("rollback_done", dir)
	log.PauseIfWindows()
}
