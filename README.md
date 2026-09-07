# GoTab

A macOS window switcher written in Go. Clean-slate rewrite of [AltTab](https://alt-tab.app/) — new bundle
ID, nothing inherited from an existing install.

**Status: Phase 0 (proof).** Not usable yet. The design is being validated before the app is built; see
[docs/ROADMAP.md](docs/ROADMAP.md) for exactly where it stands.

## Install

Requires macOS 12+, Go, and Xcode command line tools.

```bash
git clone <this repo> && cd gotab
./scripts/install.sh
```

The installer builds a universal binary, packages `GoTab.app`, installs it to `/Applications`, and then
tells you how to grant the two permissions macOS requires (Accessibility, Screen Recording). It is safe to
re-run — it upgrades in place.

## For developers

```bash
./scripts/build.sh     # build build/GoTab.app
./scripts/check.sh     # the gate: gofmt, vet, tests, architecture invariants
./scripts/wt.sh new P1.3   # worktree for parallel agent work
```

## Documentation

Read in this order:

| file | what it is |
|---|---|
| [AGENTS.md](AGENTS.md) | the working agreement: commands, Go style, workflow. `CLAUDE.md` includes it. |
| [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) | the invariants. Read before touching code. |
| [docs/ROADMAP.md](docs/ROADMAP.md) | every task and its state. The source of truth for "what's done". |
| [docs/ALTTAB-LESSONS.md](docs/ALTTAB-LESSONS.md) | prior art: AltTab platform knowledge and the traps it hit. |
| [docs/DECISIONS.md](docs/DECISIONS.md) | what was measured and what it forced. Append-only. |
| [docs/PARALLEL-WORK.md](docs/PARALLEL-WORK.md) | how to parallelise this without wasting tokens. |

## What's already known

Measured, not assumed — details in [DECISIONS.md](docs/DECISIONS.md):

- cgo crossings cost **31 ns out / 39 ns back**, ~12–15x a native call, so everything is batched
- The toolchain forces a **macOS 12 minimum** (D17; D2 measured 11.0 on an older Go). 10.14/10.15 are
  permanently out of scope
- `CGWindowListCreateImage` is **obsoleted in macOS 15** — capture must use ScreenCaptureKit
- Window enumeration costs **0.30 ms warm** for ~18 windows in one call — comfortably within budget
- CoreGraphics bitmap memory **is** measurable — `vmmap --summary` -> `CG raster data` plus
  `Physical footprint (peak)`, implemented in `spike/procmem`. See D8.
- macOS reclaims idle thumbnail pages on its own, so a bounded cache buys **peak footprint and fault-in
  latency**, not a smaller steady state. See D9.
