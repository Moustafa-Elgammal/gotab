# GoTab

A macOS window switcher written in Go. Built from scratch — its own bundle ID, nothing inherited
from any existing install.

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

## Releases

Pushing a `v*` tag is the whole release process — `.github/workflows/release.yml` runs the gate,
builds the universal `.app`, and cuts a [GitHub Release](https://github.com/Moustafa-Elgammal/gotab/releases)
with the zipped bundle and its SHA-256:

```bash
git tag -a v0.2.0 -m "Multi-monitor fixes and a faster first summon."
git push origin v0.2.0
```

The same workflow publishes `latest.json` to GitHub Pages
(`https://moustafa-elgammal.github.io/gotab/latest.json`), which is where `gotab -check-update` looks
to tell you a newer build is out. The bundle is ad-hoc signed, so a copy downloaded to another Mac
needs a right-click → Open the first time (notarization is future work). See
[docs/DECISIONS.md](docs/DECISIONS.md) D42.

## License

GoTab is free software: you can redistribute it and/or modify it under the terms of the **GNU General
Public License, version 3 or (at your option) any later version**, as published by the Free Software
Foundation. The full text is in [LICENSE](LICENSE).

GoTab is distributed in the hope that it will be useful, but WITHOUT ANY WARRANTY; without even the
implied warranty of MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the GNU General Public
License for more details.

Copyright (C) 2026 Moustafa Elgammal

## Documentation

Read in this order:

| file | what it is |
|---|---|
| [AGENTS.md](AGENTS.md) | the working agreement: commands, Go style, workflow. `CLAUDE.md` includes it. |
| [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) | the invariants. Read before touching code. |
| [docs/ROADMAP.md](docs/ROADMAP.md) | every task and its state. The source of truth for "what's done". |
| [docs/PLATFORM-LESSONS.md](docs/PLATFORM-LESSONS.md) | prior art: macOS window-switcher platform knowledge and the traps to avoid. |
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
