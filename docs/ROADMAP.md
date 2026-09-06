# Roadmap

**Single source of truth for what is done.** Update this file in the same commit as the work. A task is not
done until its box is `[x]` here.

Status: `[ ]` todo · `[~]` in progress · `[x]` done · `[!]` blocked · `[-]` dropped

Each task: `ID · title · branch · acceptance criterion`. If a task has no measurable acceptance criterion,
it is not ready to start.

---

## Phase 0 — Proof (the gate)

Nothing else starts until these pass. Purpose: find out whether Go+cgo can drive this at all, cheaply,
before committing tens of thousands of lines. **A failed gate is a successful phase.**

- [ ] **P0.1** cgo + NSPanel spike — `spike/panel` — a borderless panel shows from Go; measure summon→pixels
- [ ] **P0.2** CGEventTap hotkey from Go — `spike/hotkey` — ⌥⇥ captured; measure C→Go callback latency
- [x] **P0.3** Batched window enumeration — `spike/memory` — **done: 57 ms cold, 0.30 ms warm** for 18 windows
      in one cgo call. Inside budget. See D5 — the cold cost must be paid at launch, not first summon.
- [!] **P0.4** Thumbnail hold/release — **BLOCKED, split.** The naive instruments are blind to CoreGraphics
      memory: 366 MB of bitmaps showed as ~8 MB of growth. See D4.
  - [ ] **P0.4a** Build a trustworthy memory instrument — `vmmap` regions / `footprint` CLI / Instruments —
        acceptance: **it reports ~366 MB for `spike/memory` as it stands.** Nothing else in this project is
        credible until this passes.
  - [ ] **P0.4b** Re-run hold/release against real ScreenCaptureKit output, using P0.4a's instrument
- [ ] **P0.6** ScreenCaptureKit capture prototype — `spike/sck` — CGWindowListCreateImage is gone (D3); this
      is the highest-risk unknown in the whole port and belongs in Phase 0, not Phase 2
- [ ] **P0.5** Write up results in `docs/DECISIONS.md`, decide go/no-go

**Gate criteria (all must hold):**
| metric | budget | why |
|---|---|---|
| summon → pixels on screen | < 100 ms | AltTab's felt responsiveness |
| hotkey callback latency | < 5 ms | keystroke must not feel dropped |
| RSS after capture+release cycle | baseline ± 5 MB | proves the memory rule is achievable |
| steady-state RSS, 50 windows | < AltTab's | the entire point of the project |

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
