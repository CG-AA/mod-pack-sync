# mod-pack-sync (maintainer / developer guide)

Two small Go programs that keep a CurseForge-style modpack's **customizations**
in sync between machines. You drop the binaries into the modpack's **instance
root folder**; they operate on that folder, so the tool is launcher- and
OS-agnostic and works on any pack (not just MCE2).

For the non-technical end-user instructions (Traditional Chinese), see
[`使用說明-zh-TW.md`](使用說明-zh-TW.md).

## How it works

We never re-ship the multi-GB base pack. Instead the sender computes a **delta
against a fresh-install baseline** and transfers only that:

- **Write** = files that are new or changed vs. the baseline (your added mods,
  edited `config/`, `kubejs/`, resource/shader packs, …).
- **Delete** = base files the sender removed — each carrying its expected
  pristine hash.

The receiver applies the delta onto its own **fresh install**.

**Sync semantics:** union with sender-priority. Apply writes/overwrites the
sender's files and removes base files the sender deleted, but **never touches
files the receiver added on their own**. It is a one-directional push per run,
not a three-way merge.

Transfer is **peer-to-peer via [magic-wormhole]** (a short code phrase, no
accounts), with a **file fallback** for blocked networks or offline handoff.

[magic-wormhole]: https://github.com/psanford/wormhole-william

## Layout

```
cmd/modpack-send/      sender:   send (default) + capture-baseline
cmd/modpack-receive/   receiver: receive (default) + rollback
internal/manifest/     baseline + delta JSON formats
internal/scan/         file walk + sha256 + exclude matcher
internal/delta/        Compute / Pack / Apply / Rollback
internal/transport/    magic-wormhole send/receive
internal/root/         resolve instance root + optional settings
internal/i18n/         en / zh-TW message catalog
internal/logx/         localized output teed to a log file
```

All runtime state lives under `<root>/.modpack-sync/` (and is excluded from the
sync): `baseline.json`, `settings.json`, `backups/<ts>/`, `logs/<ts>.txt`.

## Build

Requires Go 1.24+.

```sh
# native build of both programs
go build ./cmd/modpack-send ./cmd/modpack-receive

go test ./...
```

The output is a single static binary per program (no runtime dependencies).

### Release builds (all platforms)

`scripts/build-release.sh` cross-compiles both programs for Windows, Linux, and
macOS and packages each platform into one archive (plus `checksums.txt`) under
`dist/`:

```sh
VERSION=v1.0.0 ./scripts/build-release.sh   # VERSION defaults to "dev"
```

### Cutting a release

`.github/workflows/release.yml` publishes releases automatically:

1. Merge your changes to `main`.
2. Tag and push: `git tag v1.0.0 && git push origin v1.0.0`.

The workflow builds every platform and attaches the archives to a new GitHub
Release. Windows users grab `modpack-sync-<version>-windows-amd64.zip` (it holds
both `.exe` plus these docs) from the repo's **Releases** page.

## The baseline (the one operational step)

The sender needs `baseline.json` — a hashed snapshot of a **fresh** install of
the pack. Workflow, done once per pack version by a technical user:

1. Install the pack clean (no customizations yet) via CurseForge.
2. Drop `modpack-send` into that instance folder and run:
   ```sh
   modpack-send capture-baseline --label "MCE2 <version>"
   ```
   → writes `.modpack-sync/baseline.json`.
3. Commit that file and **ship it next to the binaries** so every sender has it.

Only the **sender** consumes the baseline; the receiver needs none. If the
baseline is stale (pack updated), syncing still works — customizations are sent
as normal and only now-invalid deletes are skipped and reported ("kept N"). It
is never destructive.

## Commands

```sh
# capture a fresh-install snapshot (run on a clean install)
modpack-send capture-baseline [--label TEXT] [--version TEXT]

# send your customizations over wormhole (prints a code to share)
modpack-send

# ... or save the delta to a file instead (offline handoff)
modpack-send --out delta.zip

# receive over wormhole (prompts for the code) and apply
modpack-receive

# ... or apply a delta from a file
modpack-receive --in delta.zip

# undo the most recent apply from backup
modpack-receive rollback
```

Common flags: `--root DIR` (default: the program's own folder), `--lang en|zh-TW`
(default: auto-detect, falling back to Traditional Chinese), `--relay URL`.

## Settings (optional)

`<root>/.modpack-sync/settings.json` — for the double-click user who can't pass
flags. Everything is optional; zero config works.

```json
{
  "lang": "zh-TW",
  "label": "MCE2",
  "relay": "",
  "excludes": ["mods/clientonly-mod.jar", "config/some-personal.toml"]
}
```

## Excludes

Saves, logs, caches, per-user prefs (`options.txt`, `servers.dat`, …) and the
tool's own files are never synced. See `DefaultExcludes` in
`internal/scan/scan.go`. Patterns: `dir/` (directory), exact path, or `*.glob`.
Add pack-specific entries via `settings.json`.

## Safety

- Every overwritten/deleted file is copied to `.modpack-sync/backups/<ts>/`
  before any change; the applied manifest is saved alongside it.
- `modpack-receive rollback` restores the latest backup exactly, including
  removing files the apply newly created.
- Deletes are hash-guarded: a base file is only removed if it still matches the
  baseline, so receiver-modified base files are kept.
- Package paths are validated to stay inside the instance root (no `..`/abs
  traversal).

## Troubleshooting

- **No code appears when sending / receive hangs:** the network is blocking the
  public wormhole relay (`relay.magic-wormhole.io`). Use the file fallback
  (`--out` / `--in`), or set a reachable `relay` in `settings.json`.
- **Windows SmartScreen warns about the unsigned `.exe`:** expected; code
  signing is not yet set up.
- **Logs:** every run writes `.modpack-sync/logs/<timestamp>.txt`.
