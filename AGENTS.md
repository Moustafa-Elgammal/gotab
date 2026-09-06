# GoTab — working agreement

A macOS window switcher in Go. Clean-slate rewrite of AltTab; nothing is inherited from an existing
install. **Status: Phase 0 (proof). The app does not switch windows yet.**

## Read these first

| file | when |
|---|---|
| `docs/ARCHITECTURE.md` | before touching any code — the invariants live there, not here |
| `docs/ROADMAP.md` | to find out what is done and what is next. The source of truth. |
| `docs/DECISIONS.md` | when something looks arbitrary. It records what was measured. Append-only. |
| `docs/PARALLEL-WORK.md` | only when splitting work across agents or worktrees |

Rules are stated once, at the widest scope they apply to. The architectural invariants (cgo batching, the
memory-ownership rule, the `core` purity rule, threading) live in `docs/ARCHITECTURE.md` and are **not**
repeated here — restating them is what makes two copies drift apart.

## Commands

| task | command |
|---|---|
| the gate — run before calling anything done | `./scripts/check.sh` |
| build `build/GoTab.app` | `./scripts/build.sh` |
| install for real use | `./scripts/install.sh` |
| remove it | `./scripts/uninstall.sh` |
| one test | `go test ./internal/core/... -run TestFoo` |
| allocation check on a hot path | `go test ./internal/core/... -bench . -benchmem` |
| worktree for a parallel task | `./scripts/wt.sh new P1.3` |

`check.sh` runs gofmt, vet, tests, and mechanically enforces that `internal/core` imports neither cgo nor
`internal/platform`. Green here is a precondition for merging, not a nicety.

## Go style

- Standard Go: `gofmt`, short receiver names, errors returned not logged, `context.Context` first
  parameter where it appears at all.
- **Share by communicating.** Mutable state lives in one event-loop goroutine. If you reach for a mutex
  around the window model, the design is wrong — say so rather than adding the mutex.
- **No allocation on the hot path.** Summon, keystroke-while-cycling, and dismissal are latency-critical.
  Preallocate and reuse; prove it with `-benchmem` showing 0 allocs/op.
- Prefer a small concrete type over an interface. Introduce an interface at the point a second
  implementation actually exists, not in anticipation of one.
- Package names are lowercase and meaningful; no `util`, `common`, or `helpers`.
- Every package under `internal/` has a `doc.go` stating its invariants in prose. Agents read that
  instead of the source, so keep it accurate.

## Comments

A wrong comment costs several times more than a missing one. Write for low drift, not low line count.

- Comment what the code cannot show: macOS/CoreGraphics/AppKit behaviour, measured timings, cgo lifetime
  rules, and why a guard that looks removable isn't. Prefer measured evidence over recollection.
- Don't narrate history and don't restate what the code says. If a past bug is why a constraint exists,
  name the test that pins it.
- When you change code, re-read the comments around it. Updating them is part of the change.

## Workflow

- **A task is done only when `docs/ROADMAP.md` is updated in the same commit.** That file is how a cold
  session learns where things stand; skipping it is how this project gets lost.
- Anything measured or surprising gets appended to `docs/DECISIONS.md` with its numbers.
- Conventional commit messages (`feat:`, `fix:`, `perf:`, `chore:`), written for a changelog reader.
- Never report a gate as passing on a number you don't believe. Phase 0 already produced one false
  `GATE PASS` that had to be retracted — an honest `INCONCLUSIVE` is worth more than a green light.

## Scope discipline

Phase 0 is a gate, not a formality. If a spike shows the design can't hit its budget, the correct outcome
is to change the design or stop — not to proceed and hope. A negative result that is *correct* is a
successful Phase 0.
