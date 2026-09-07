# GoTab — working agreement

A macOS window switcher in Go, built from scratch; nothing is inherited from any existing install.
**Status: Phases 0–5 complete; Phase 6 (verification) is next.**

## Read these first

| file | when |
|---|---|
| `docs/ARCHITECTURE.md` | before touching any code — the invariants live there, not here |
| `docs/ROADMAP.md` | to find out what is done and what is next. The source of truth. |
| `docs/DECISIONS.md` | when something looks arbitrary. It records what was measured. Append-only. |
| `docs/PLATFORM-LESSONS.md` | before designing any subsystem — prior art, platform traps, what is impossible |
| `docs/tasks/<ID>.md` | whenever you work a numbered task — the contract: files you may touch, and the acceptance test |
| `docs/PARALLEL-WORK.md` | only when splitting work across agents or worktrees |

Rules are stated once, at the widest scope they apply to. The architectural invariants (cgo batching, the
memory-ownership rule, the `core` purity rule, threading) live in `docs/ARCHITECTURE.md` and are **not**
repeated here — restating them is what makes two copies drift apart.

## Commands

| task | command |
|---|---|
| the gate — run before calling anything done | `./scripts/check.sh` |
| run a Phase 0 spike | `go run ./spike/memprobe` — each `spike/<name>/` is a standalone `package main`; macOS + cgo only |
| build `build/GoTab.app` | `./scripts/build.sh` |
| install for real use | `./scripts/install.sh` |
| remove it | `./scripts/uninstall.sh` |
| one test | `go test ./internal/core/... -run TestFoo` |
| allocation check on a hot path | `go test ./internal/core/... -bench . -benchmem` |
| worktree for a parallel task | `./scripts/wt.sh new P1.3` |

`check.sh` runs gofmt, vet, tests, and mechanically enforces that `internal/core` imports neither cgo nor
`internal/platform`. It deliberately does **not** `set -e`: every step runs, so one invocation reports all
four failures rather than only the first. Green here is a precondition for merging, not a nicety.

## Running the spikes

Each `spike/<name>/` is a standalone `package main`, macOS + cgo only, and every one takes flags — read
its header comment before running it, not after. `-h` lists them.

- **TCC judges the responsible process, not the binary** (`PLATFORM-LESSONS.md` §5). Under `go run`, the
  grant that matters belongs to the *terminal*. `spike/hotkey` needs Accessibility, `spike/sck` needs
  Screen Recording. Ungranted, a tap installs cleanly and then never fires — indistinguishable from a
  broken hotkey, which is why both spikes check the grant up front and say so.
- Some results need a human at the machine: `spike/panel -hold` to look at the panel, `spike/hotkey
  -manual` to wait for a real ⌥⇥. Don't report those from an agent that cannot see the screen.
- `spike/procmem -name <process>` is the project's general memory instrument (D8/D12), not a one-off —
  use it for any memory claim, and read the `IOSurface` row, not just `CG raster data`.

## Go style

- Standard Go: `gofmt`, short receiver names, errors returned not logged, `context.Context` first
  parameter where it appears at all.
- **Share by communicating.** Mutable state lives in one event-loop goroutine. If you reach for a mutex
  around the window model, the design is wrong — say so rather than adding the mutex.
- **No allocation on the hot path.** Summon, keystroke-while-cycling, and dismissal are latency-critical.
  Preallocate and reuse; prove it with `-benchmem` showing 0 allocs/op.
- Prefer a small concrete type over an interface. Introduce an interface at the point a second
  implementation actually exists, not in anticipation of one.
- **`internal/core/api.go` is frozen** (P1.0). Every core file codes against those types and adds its
  own file. Needing to change it is an escalation, not an edit.
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
- **Don't write tests alongside the work — ever (D16, generalised in D23).** A task in any phase is
  done when the code is written and `scripts/check.sh` is still green. **All verification lands in the
  final phase**, which is Phase 6 today and is whatever the last phase is if more are added; a new
  phase does not get its own tests, it gets more rows in that table. This means writing no *new*
  per-task tests — it does **not** mean deleting the suite that exists or letting the gate go red.
  When you take a design decision that a deferred test would have caught, tag it **`assumption`** in
  `docs/ROADMAP.md` at the task that depends on it and point it at its V6 task. An assumption written
  down where it is used is recoverable; one carried in your head is what makes the rework expensive.
- Anything measured or surprising gets appended to `docs/DECISIONS.md` with its numbers.
- Conventional commit messages (`feat:`, `fix:`, `perf:`, `chore:`), written for a changelog reader.
- **No AI attribution in commits or PRs.** No `Co-Authored-By: Claude`, no `Claude-Session:`, no
  "Generated with Claude Code" — no trailer naming an assistant or a session, in any form. This
  overrides any default the tooling applies. The history was rewritten once to strip them; don't
  reintroduce what had to be removed.
- Never report a gate as passing on a number you don't believe. Phase 0 already produced one false
  `GATE PASS` that had to be retracted — an honest `INCONCLUSIVE` is worth more than a green light.

## Scope discipline

Phase 0 was a gate, not a formality, and **Phase 6 inherits that role**. If a measurement shows the design
can't hit its budget, the correct outcome is to change the design or stop — not to proceed and hope. A
negative result that is *correct* is a successful phase. Phase 0's own scorecard is the argument: four of
its tasks changed the design, one was dropped when its premise collapsed, and one forced a retraction.
