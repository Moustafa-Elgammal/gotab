# Roadmap

**Single source of truth for what is done.** Update this file in the same commit as the work. A task is not
done until its box is `[x]` here.

Status: `[ ]` todo · `[~]` in progress · `[x]` done · `[!]` blocked · `[-]` dropped

Each task: `ID · title · branch · acceptance criterion`. If a task has no measurable acceptance criterion,
it is not ready to start.

---

## Phase 0 — Proof (the gate)

Purpose: settle the platform unknowns that would change the design, before Phase 2 is planned in detail.
**This is de-risking, not a go/no-go** — the project is committed to building the switcher (D10). A Phase 0
task that comes back negative changes the approach; it does not stop the work.

- [x] **P0.1** cgo + NSPanel spike — `spike/panel` — **done (D13): 1.3 ms warm, 14 ms cold — ~1% of the
      100 ms budget.** Borderless non-activating panel, 8 tiles in one view, verified on screen by
      screenshot; the frontmost app never changed across 20 summons. "The call returned" is ~1 ms while
      the cold frame commits at ~14 ms, so measuring the call would have been wrong by 10x.
      **Still open, needs a human:** behaviour over a full-screen app and across Spaces.
- [ ] **P0.2** CGEventTap hotkey from Go — `spike/hotkey` — ⌥⇥ captured; measure C→Go callback latency
- [x] **P0.3** Batched window enumeration — `spike/memory` — **done: 57 ms cold, 0.30 ms warm** for 18 windows
      in one cgo call. Inside budget. See D5 — the cold cost must be paid at launch, not first summon.
- [~] **P0.4** Thumbnail hold/release — split. Not a competitive target any more (D10), but leaking
      bitmaps is still a real bug. P0.6 has now shown release working (D12); what P0.4b still needs is an
      instrument that can see `IOSurface`, because P0.4a built one that cannot.
  - [x] **P0.4a** Build a trustworthy memory instrument — `vmmap` regions / `footprint` CLI / Instruments —
        **DONE (D8):** the instrument is `vmmap --summary` -> the `CG raster data` row plus
        `Physical footprint (peak)`. Reports 368.8 MB / 374.4 MB peak against 366.2 MB declared.
        Implemented in `spike/memprobe`. D4's "instruments are blind" conclusion was wrong.
  - [ ] **P0.4b** Re-run hold/release against real ScreenCaptureKit output — acceptance is **no growth
        across 100 capture/release cycles**. P0.6 did 60 hold/release and 10 cycles with clean release, so
        this is now close to a formality — but **P0.4a's instrument is the wrong one** (D12): SCK output
        lands in `IOSurface`, not `CG raster data`, and `spike/procmem` must learn that row first.
- [x] **P0.6** ScreenCaptureKit capture prototype — `spike/sck` — **done (D12): it works, and it is too
      slow to use on summon.** Capture is ~46 ms warm / ~112 ms cold, plus ~46 ms to enumerate, and it
      does **not** parallelise (10 at once = 324 ms vs 460 ms serial). The completion handler does fire
      on a Go thread with no run loop, so Phase 2 needs no AppKit marshalling. Thumbnails are
      IOSurface-backed, invisible to footprint, and release cleanly (330.5 MB -> 64 K).
- [-] **P0.7** Measure AltTab actual memory — **DROPPED (D10):** beating AltTab on memory is no longer a
      goal, so the baseline has nothing to serve. It was measured far enough to be worth keeping (D10's
      numbers) before it was dropped. `spike/procmem` survives it and is the general memory instrument.
- [ ] **P0.5** Write up results in `docs/DECISIONS.md` — confirm the panel, hotkey and capture unknowns
      are settled and Phase 2 can be planned against real numbers. Capture (D12) and the panel (D13) are
      settled; **P0.2 is the last one.**

**Phase 0 targets (all must hold):**
| metric | budget | why |
|---|---|---|
| summon → pixels on screen | < 100 ms | the switcher has to feel instant; this is the one that matters |
| hotkey callback latency | < 5 ms | keystroke must not feel dropped |
| capture → release, 100 cycles | no net growth | proves bitmaps are actually released — a leak here compounds |
| thumbnail cache | bounded, bound is a number | predictable peak and a warm cache on summon (D9, D10) |

The old fourth criterion, "steady-state RSS, 50 windows < AltTab's", is gone with D10. It was also
unmeasurable as written: macOS drives idle thumbnail memory to ~0 on both sides.

---

## Phase 1 — Core model (pure Go, no cgo, fan out freely)

Independent, testable, no macOS needed. **This is where multi-agent parallelism pays off** — see
[PARALLEL-WORK.md](PARALLEL-WORK.md). Interfaces in `internal/core/api.go` are frozen before any agent starts.

- [x] **P1.0** `internal/core/api.go` — freeze the types & interfaces every other P1 task codes against
- [x] **P1.1** Window/App model — struct-of-arrays, zero alloc on the hot path — `go test -benchmem` shows 0 allocs/op
- [x] **P1.2** MRU ordering kernel — only an attention decision or structural repair may reorder
- [x] **P1.3** Filter kernel — minimized / hidden / other-Space / per-app rules, table-driven
- [x] **P1.4** Search & fuzzy match — scoring is deterministic and covered by a fixture table
- [x] **P1.5** Selection resolver — cycle, wrap, arrows; selection survives list reordering
- [x] **P1.6** Tab-group model — group membership, representative election
- [x] **P1.7** Thumbnail cache policy — bounded LRU, eviction order proven by test (no bitmaps, just policy)

---

## Phase 2 — Platform bridge (serial, one owner)

Shares the C shim and the main thread. **Do not fan out.** One agent, sequential.

- [ ] **P2.1** C shim skeleton + cgo build integration
- [ ] **P2.2** Batched window query — one call → packed array
- [ ] **P2.3** AX observer registration; callbacks enqueue only
- [ ] **P2.4** SkyLight/CGS notification tap
- [ ] **P2.5** Focus / raise / minimize / close actions
- [ ] **P2.6** Thumbnail capture with explicit C-side lifecycle — wired to P1.7's policy. **Captures
      ahead of summon, never during it** (D12), and must treat a capture as failable and time-bounded

---

## Phase 3 — UI (serial)

- [ ] **P3.1** Panel + **single-view renderer** — AltTab has 53 NSView subclasses; we draw all tiles in one
      view to keep the C→Go callback count near zero. Must render a tile with **no thumbnail yet** and
      fill it in asynchronously — D12 makes that a launch requirement, not a refinement
- [ ] **P3.2** Tile layout engine — pure Go computes frames, C only draws
- [ ] **P3.3** Thumbnail rendering via CALayer contents
- [ ] **P3.4** Theme / appearance / dark mode

---

## Phase 4 — Product

- [ ] **P4.1** Preferences — plist under the new bundle ID
- [ ] **P4.2** Settings UI
- [ ] **P4.3** Permissions onboarding — Accessibility + Screen Recording
- [ ] **P4.4** Multi-monitor & Spaces
- [ ] **P4.5** `.app` bundle packaging, ad-hoc codesign, `install.sh`

---

## Phase 5 — Polish

- [ ] **P5.1** Localization scaffold
- [ ] **P5.2** VoiceOver / accessibility
- [ ] **P5.3** Update mechanism (decide: Sparkle via cgo, or plain download)

---

## Log

Append one line per session. Newest last. This is how a cold session learns what happened.

- `2026-09-06` — Project created. Measured cgo overhead (31 ns out / 39 ns callback), confirmed Go forces
  `minos 11.0`, established that thumbnails are CoreGraphics-owned so the memory win must come from cache
  policy. Phase 0 defined as a hard gate.
- `2026-09-06` — Ran `spike/memory`. **P0.3 passes** (0.30 ms warm enumeration). **P0.4 blocked**: neither
  `phys_footprint` nor `resident_size` can see CoreGraphics bitmap memory, so the reclaim gate is unproven
  and the spike's original "PASS" was wrong — removed. Split into P0.4a (build an instrument) / P0.4b.
  Also found `CGWindowListCreateImage` is obsoleted in macOS 15, forcing ScreenCaptureKit — added P0.6.
  **Next session starts at P0.4a.**
- `2026-09-06` — Handoff. Added `docs/ALTTAB-LESSONS.md` (AltTab platform knowledge, distilled with
  sources), D6 (project framing and rejected alternatives) and D7. Fixed a broken build (`cmd/gotab` was
  empty) and a false-positive purity gate. Added P0.7: **AltTab memory was never measured, so the
  primary goal has no baseline.**
  **Next session: start with P0.4a (`docs/tasks/P0.4a.md`), then P0.7.** Those two together decide whether
  the memory premise holds; everything else is downstream of that answer.
- `2026-09-06` — **P0.4a done.** Built `spike/memprobe`, which tested three hypotheses and rejected all of
  them before finding the real answer: `vmmap` has a dedicated `CG raster data` region that accounts our
  bitmaps exactly (368.8 MB of 366.2 MB declared), and `Physical footprint (peak)` caught the true
  374.4 MB high-water mark. Instantaneous `phys_footprint` misses it because macOS reclaims idle CG raster
  pages. Corrected D4 in D8.
  **D9 is the uncomfortable part: macOS already evicts idle thumbnails, so the bounded-LRU win may be
  small.** That makes P0.7 (measure AltTab) the deciding task — do it before designing the cache.
  **Next session: P0.7.**
- `2026-09-06` — Docs/tooling repair, no code change. The repo's only branch is `main`, but `scripts/wt.sh
  done` ran `git checkout master`, CI's push trigger watched `master`, and `PARALLEL-WORK.md` said the
  same — the first merged worktree would have failed. All switched to `main`. Also documented how to run a
  spike (`go run ./spike/<name>`), why `check.sh` omits `set -e` (all four steps report, not just the
  first), and noted in `ARCHITECTURE.md` that its four-layer diagram is the target, not the current tree —
  only `cmd/gotab` and `spike/` exist today. Fixed two bugs in `wt.sh`'s usage output while there: the
  line range leaked `set -euo pipefail`, and `sed 's/^# \?//'` is a GNU-ism that BSD sed reads as a
  literal `?`, so it never stripped the comment prefixes on the one platform this project supports.
  **Next session: still P0.7 (measure AltTab).**
- `2026-09-06` — **P0.7 started, not finished.** Built `spike/procmem`: D8's three quantities
  (`CG raster data`, footprint, peak) read out of `vmmap --summary` for *any* pid, since `spike/memprobe`
  can only measure itself. Validated against a holder process with a known 366.2 MB — reports 368.8 MB
  virtual, matching D8 exactly. Installed AltTab 11.6.0 and captured its idle baseline: **27.4 MB
  footprint, 28.4 MB peak, and no `CG raster data` region at all** (it had never been summoned).
  Two things learned that P0.7's contract now records: `CG raster` RESIDENT decays fast — the same
  366 MB reads 368.8 MB when sampled immediately after a touch and **6.4 MB** ~15 s later — so summon-time
  sampling is mandatory and `Physical footprint (peak)` is the only non-decaying number worth quoting.
  And AltTab relaunches itself under a new pid on permission grant, which silently killed the first
  90-sample run; `-name` now re-resolves every sample.
  **Next session: the measurement still needs a human** to grant Accessibility + Screen Recording and
  press ⌥⇥ (TCC blocks synthetic keystrokes). Protocol is in `docs/tasks/P0.7.md`.
- `2026-09-06` — **Direction change: memory parity with AltTab is no longer a goal (D10).** The project
  is now feature parity with AltTab's core switching, written in Go, on a sane memory budget. P0.7 dropped,
  Phase 0 reframed from a go/no-go gate to de-risking the three platform unknowns (panel, hotkey,
  ScreenCaptureKit), and the AltTab-relative gate criterion replaced with a leak check and a bounded-cache
  requirement. `spike/procmem` survives as the general memory instrument for any pid.
  **Next session: P0.1 (NSPanel spike), P0.2 (hotkey), P0.6 (ScreenCaptureKit)** — P0.6 is the highest-risk
  of the three (D3) and gates the capture design in Phase 2.
- `2026-09-06` — **Phase 1 complete.** P1.0 frozen first and serially (`internal/core/api.go`, types only),
  then P1.1-P1.7 built by seven agents in parallel — the fan-out `PARALLEL-WORK.md` reserves for this phase.
  ~3,300 lines in `internal/core`, every hot path 0 allocs/op. The five cross-task integration tests passed
  on first run, so the seven pieces composed without adjustment.
  Two defects in the contract I wrote, both caught by the agents coding against it, both fixed here:
  the Space rule guarded `Rules.CurrentSpace == 0` but not a window's own unknown Space (P1.3 extended it
  symmetrically), and `Cache.Capacity` was an exported field whose "must be > 0" invariant only held at
  construction — unexported in D11.
  **Next session: Phase 2, and it does NOT fan out** — one owner, sequential, shared C shim and main
  thread. Start with P0.6 (ScreenCaptureKit) if Phase 0's remaining spikes are still open: it is the
  highest-risk unknown and P2.6 is designed against whatever it finds.
- `2026-09-06` — **P0.6 done, and it changed the design.** ScreenCaptureKit captures fine from Go: the
  completion handler fires on a Go-owned thread with no NSRunLoop, so Phase 2 needs no AppKit
  marshalling — the port's biggest threading risk turned out not to be real. The two findings nobody
  asked for matter more (D12). **Capture is ~46 ms of fixed WindowServer round-trip and does not
  parallelise** — a full-res image costs the same as a 400 px one, and 10 concurrent captures cost 324 ms
  against 460 ms serial. With a 100 ms summon budget and 46 ms just to enumerate, **thumbnails cannot be
  captured on summon**: P2.6 must capture ahead of time and P3.1 must render tiles that have no thumbnail
  yet. And **SCK output is IOSurface-backed, not `CG raster data`** — 330.5 MB of held surfaces read as
  7.1 MB of process footprint, so P0.4a's instrument is blind to exactly the thing this project needs to
  bound. Release is clean (330.5 MB -> 64 K). Also found that CoreGraphics `abort()`s a non-AppKit
  process on its first capture unless `CGMainDisplayID()` is called first — that will bite P0.1 and P0.2
  too. One capture timed out entirely in ~10 runs, unreproducibly: SCK can simply not answer.
  **Next session: P0.1 (NSPanel) and P0.2 (hotkey)**, the last two Phase 0 unknowns, then P0.5 closes the
  phase. `spike/procmem` needs an `IOSurface` row before P0.4b can run.
- `2026-09-06` — Project identity and paths made self-contained, ahead of publishing the repo. The module
  is now `github.com/Moustafa-Elgammal/gotab` (nothing imported the old path, so this was one line).
  `docs/ALTTAB-LESSONS.md` cited AltTab at a personal absolute path; it now cites the upstream URL and
  says the checkout can live anywhere. Worktrees moved from the sibling `../gotab-wt/` into `.worktrees/`
  inside the repo, so a clone cannot scatter directories over its parent. **The leading dot is
  load-bearing and half of that change is in `check.sh`:** `go list`, `go vet` and `go build ./...` skip
  dot-directories, but `gofmt -l .` walks them, so the gate would have failed on another task's
  half-written code. gofmt is now handed the package list from `go list`. Verified both directions —
  a worktree holding unformatted code leaves the gate green, and unformatted code in the project itself
  still fails it.
- `2026-09-06` — **P0.1 done, and it is the first Phase 0 target with room to spare.** `spike/panel`
  puts a borderless, non-activating NSPanel on screen from Go — verified by screenshot, not by the
  absence of an error — with eight tiles drawn in a single view, which is P3.1's shape rather than a
  flattering empty window. **1.3 ms warm, 14 ms cold: about 1% of the 100 ms budget.** Set against D12's
  ~46 ms capture, this settles where the summon budget goes: on data, not on drawing.
  Two things the measurement had to defend against. `orderFrontRegardless` returns in ~1 ms while the
  cold frame does not commit for ~14 ms, exactly as ALTTAB-LESSONS predicted, so timing the call would
  have been wrong by 10x — the spike reports three timestamps instead. And `drawRect:` is instrumented
  to prove the warm number is a real re-render: 0 of 30 warm summons skipped the draw. The
  non-activating combination works — the frontmost app's pid never changed across 20 summons.
  **One thing is deliberately not claimed:** behaviour over a full-screen app and across Spaces. The
  `collectionBehavior` flags are AltTab's prior art, not a measurement, and TCC blocks driving either
  synthetically. That needs a human and is the open half of P0.1.
  **Next session: P0.2 (CGEventTap hotkey), the last Phase 0 unknown**, then P0.5 closes the phase and
  Phase 2 begins — serial, one owner.

