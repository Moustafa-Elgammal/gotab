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

- [ ] **P0.1** cgo + NSPanel spike — `spike/panel` — a borderless panel shows from Go; measure summon→pixels
- [ ] **P0.2** CGEventTap hotkey from Go — `spike/hotkey` — ⌥⇥ captured; measure C→Go callback latency
- [x] **P0.3** Batched window enumeration — `spike/memory` — **done: 57 ms cold, 0.30 ms warm** for 18 windows
      in one cgo call. Inside budget. See D5 — the cold cost must be paid at launch, not first summon.
- [~] **P0.4** Thumbnail hold/release — split. Not a competitive target any more (D10), but leaking
      bitmaps is still a real bug: the instrument exists, so P0.4b just has to prove release works.
  - [x] **P0.4a** Build a trustworthy memory instrument — `vmmap` regions / `footprint` CLI / Instruments —
        **DONE (D8):** the instrument is `vmmap --summary` -> the `CG raster data` row plus
        `Physical footprint (peak)`. Reports 368.8 MB / 374.4 MB peak against 366.2 MB declared.
        Implemented in `spike/memprobe`. D4's "instruments are blind" conclusion was wrong.
  - [ ] **P0.4b** Re-run hold/release against real ScreenCaptureKit output, using P0.4a's instrument —
        acceptance is **no growth across 100 capture/release cycles**, not a number relative to anything else
- [ ] **P0.6** ScreenCaptureKit capture prototype — `spike/sck` — CGWindowListCreateImage is gone (D3); this
      is the highest-risk unknown in the whole port and belongs in Phase 0, not Phase 2
- [-] **P0.7** Measure AltTab actual memory — **DROPPED (D10):** beating AltTab on memory is no longer a
      goal, so the baseline has nothing to serve. It was measured far enough to be worth keeping (D10's
      numbers) before it was dropped. `spike/procmem` survives it and is the general memory instrument.
- [ ] **P0.5** Write up results in `docs/DECISIONS.md` — confirm the panel, hotkey and capture unknowns
      are settled and Phase 2 can be planned against real numbers

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

- [ ] **P1.0** `internal/core/api.go` — freeze the types & interfaces every other P1 task codes against
- [ ] **P1.1** Window/App model — struct-of-arrays, zero alloc on the hot path — `go test -benchmem` shows 0 allocs/op
- [ ] **P1.2** MRU ordering kernel — only an attention decision or structural repair may reorder
- [ ] **P1.3** Filter kernel — minimized / hidden / other-Space / per-app rules, table-driven
- [ ] **P1.4** Search & fuzzy match — scoring is deterministic and covered by a fixture table
- [ ] **P1.5** Selection resolver — cycle, wrap, arrows; selection survives list reordering
- [ ] **P1.6** Tab-group model — group membership, representative election
- [ ] **P1.7** Thumbnail cache policy — bounded LRU, eviction order proven by test (no bitmaps, just policy)

---

## Phase 2 — Platform bridge (serial, one owner)

Shares the C shim and the main thread. **Do not fan out.** One agent, sequential.

- [ ] **P2.1** C shim skeleton + cgo build integration
- [ ] **P2.2** Batched window query — one call → packed array
- [ ] **P2.3** AX observer registration; callbacks enqueue only
- [ ] **P2.4** SkyLight/CGS notification tap
- [ ] **P2.5** Focus / raise / minimize / close actions
- [ ] **P2.6** Thumbnail capture with explicit C-side lifecycle — wired to P1.7's policy

---

## Phase 3 — UI (serial)

- [ ] **P3.1** Panel + **single-view renderer** — AltTab has 53 NSView subclasses; we draw all tiles in one
      view to keep the C→Go callback count near zero
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
