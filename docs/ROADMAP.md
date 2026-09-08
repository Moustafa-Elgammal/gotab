# Roadmap

**Single source of truth for what is done.** Update this file in the same commit as the work. A task is not
done until its box is `[x]` here.

Status: `[ ]` todo · `[~]` in progress · `[x]` done · `[!]` blocked · `[-]` dropped

Each task: `ID · title · branch · acceptance criterion`. If a task has no measurable acceptance criterion,
it is not ready to start.

**Testing is deferred to Phase 6 (D16).** From here on a task in Phases 2–5 is done when the code is
written, `scripts/check.sh` is still green on what already exists, and its box is `[x]`. It does **not**
carry its own tests and does **not** have to produce a measurement. Everything that would have been
verified in place is collected in [Phase 6](#phase-6--verification) and run once, against the assembled
app rather than against seven separate spikes.

Two consequences, written down rather than discovered later:

- **Phase 1's tests stay and stay green.** Deferring means writing no *new* per-task tests; it does not
  mean deleting the suite that already exists or letting the gate go red.
- **Design decisions taken between here and Phase 6 rest on assumptions nothing has checked.** Each one
  is tagged **`assumption`** below. Phase 0 twice changed the design *because* something was measured
  (D3, D12), so this is the specific risk being accepted, and Phase 6 is where these hold or force rework.

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
- [x] **P0.2** CGEventTap hotkey from Go — `spike/hotkey` — code complete since Phase 0; **measured in
      [V6.1](#phase-6--verification) (D45): 3.2 ms worst case over 20 real ⌥⇥ presses, 64 % of the
      5 ms budget.** A session-level tap inserted at the head, `kCGEventTapDisabledByTimeout` re-enable,
      the C→Go crossing instrumented on the tap's own CoreFoundation thread, and the swallow. The first
      crossing (28 µs) is not the cold-thread outlier P0.2 feared. `INCONCLUSIVE` on a synthetic run;
      the real number needed a human — see the `-manual` protocol in `docs/tasks/P0.2.md`.
      **`assumption` discharged (D45):** the hotkey arrives inside 5 ms. Phase 2's summon path holds.
- [x] **P0.3** Batched window enumeration — `spike/memory` — **done: 57 ms cold, 0.30 ms warm** for 18 windows
      in one cgo call. Inside budget. See D5 — the cold cost must be paid at launch, not first summon.
- [x] **P0.4** Thumbnail hold/release — split, both halves done. Not a competitive target any more
      (D10), but leaking bitmaps is still a real bug, and it turned out not to be one.
  - [x] **P0.4a** Build a trustworthy memory instrument — `vmmap` regions / `footprint` CLI / Instruments —
        **DONE (D8):** the instrument is `vmmap --summary` -> the `CG raster data` row plus
        `Physical footprint (peak)`. Reports 368.8 MB / 374.4 MB peak against 366.2 MB declared.
        Implemented in `spike/memprobe`. D4's "instruments are blind" conclusion was wrong.
  - [x] **P0.4b** Re-run hold/release against real ScreenCaptureKit output — **done (D14): 100 cycles,
        `IOSurface` moves +0.0 MB.** `spike/sck -cycles 100`. The zeros are only worth something because
        the run ends by holding 20 thumbnails on purpose: the row tracks 8.3 MB of declared bytes to
        within 2% and gives all of it back, so the instrument is proven to see what it is watching rather
        than merely blind — which is the exact way D12 caught P0.4a out. `CGImageRelease` is sufficient;
        there is no separate `IOSurface` step for P2.6 to remember.
- [x] **P0.6** ScreenCaptureKit capture prototype — `spike/sck` — **done (D12): it works, and it is too
      slow to use on summon.** Capture is ~46 ms warm / ~112 ms cold, plus ~46 ms to enumerate, and it
      does **not** parallelise (10 at once = 324 ms vs 460 ms serial). The completion handler does fire
      on a Go thread with no run loop, so Phase 2 needs no AppKit marshalling. Thumbnails are
      IOSurface-backed, invisible to footprint, and release cleanly (330.5 MB -> 64 K).
- [-] **P0.7** Measure a reference switcher's actual memory — **DROPPED (D10):** beating another
      switcher on memory is no longer a goal, so the baseline has nothing to serve. It was measured far
      enough to be worth keeping (D10's numbers) before it was dropped. `spike/procmem` survives it and
      is the general memory instrument.
- [x] **P0.5** Write up results in `docs/DECISIONS.md` — **done (D15), and it does not claim a clean
      sweep.** Three of the four unknowns are settled on measurements Phase 2 can be planned against:
      capture (D12), the panel (D13), release (D14). The hotkey is **not** — P0.2 is code without a
      number — and D15 says so plainly instead of closing the phase on an assumption. Where the summon
      budget goes is nonetheless settled: ~46 ms on capture and ~1.3 ms on drawing means the budget is
      spent on data, and thumbnails cannot be captured on summon at all.

**Phase 0 targets — three of four have a number behind them:**
| metric | budget | status |
|---|---|---|
| summon → pixels on screen | < 100 ms | **budget allocated, not yet measured end to end.** Drawing 1.3 ms (D13), capture ~46 ms and unparallelisable (D12), enumeration 0.30 ms warm (D5) → the budget goes on data → **V6.3** |
| hotkey callback latency | < 5 ms | **met (D45, V6.1): 3.2 ms worst case over 20 real ⌥⇥ presses, mean 0.6 ms; first crossing 28 µs, not an outlier** |
| capture → release, 100 cycles | no net growth | **met**: +0.0 MB on `IOSurface`, with a held-20 control proving the instrument is not blind (D14) |
| thumbnail cache | bounded, bound is a number | **policy met** (P1.7, bounded LRU); the bound's real-world size is **V6.4** |

The old fourth criterion, "steady-state RSS, 50 windows below a comparable switcher's", is gone with
D10. It was also unmeasurable as written: macOS drives idle thumbnail memory to ~0 on both sides.

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

## Phase 2 — Platform bridge (complete)

P2.1 through P2.3c were built by one owner, sequentially, because every task wanted to edit `shim.h`
and `shim.m`. That was a fact about the file layout rather than about the work, and carving one file
pair per task removed it: **P2.3b, P2.4, P2.5 and P2.6 ran in parallel**, one worktree each, against a
frozen `shim.{h,m,go}` / `window.go` / `doc.go`. See PARALLEL-WORK.md for the ownership table — and
note that `ROADMAP.md`, `DECISIONS.md` and all wiring stay with the integrator, not the agents.

**All of Phase 2 is landed (D24–D27).** The platform layer enumerates the switchable set, reacts to
window events instead of a ticker, knows each window's Space, and can raise / minimize / unminimize /
close one. Nothing wires those into a summon path yet — that is Phase 3.

- [x] **P2.1** C shim skeleton + cgo build integration — **done, and it caught a shipped bug (D17).**
      `internal/platform/darwin` compiles Objective-C, links, and ships in the universal `.app`; the
      conventions every later P2/P3 task follows are fixed in `shim.h` (closed status enum → one Go
      error mapping, bulk results into a caller buffer, bitmaps as opaque handles). `gt_init` carries
      D12's `CGMainDisplayID()` trap, so the abort fires in nobody's task. `gotab -check` reports both
      TCC grants. **The find:** once real ObjC is compiled, clang and not the Go linker sets the
      deployment target — the `.app` declared macOS 11 and required macOS 26. `build.sh` now derives
      both from one variable and fails the build if a slice disagrees.
- [x] **P2.2** Batched window query — one call → packed array — **done.** `gt_window_list` fills a
      caller-allocated array of fixed-size records in one crossing regardless of window count; Go
      composes `core.Window` from it. No flag bits in C: `core.WindowFlags` is frozen and a second copy
      of those values is the drift AGENTS.md warns about, so C reports facts and Go decides. Titles
      truncate on a character boundary (`CFStringGetBytes`, not `CFStringGetCString`). Observable with
      `gotab -list`. **The find (D19): this is not the switchable set** — 59 layer-0 windows, 7 of them
      switchable.
- [x] **P2.3** AX enumeration + observers — **split three ways**, all landed. D19 moved enumeration in
      here; D20 then showed enumeration alone is not enough.
  - [x] **P2.3a** AX enumeration — `kAXWindowsAttribute` per regular application, one crossing.
        **Done: 5 switchable windows against CoreGraphics' 59 candidates, and 5 of 5 carry an ID
        CoreGraphics also reports** — which is the evidence that the private `_AXUIElementGetWindow`
        returns real window numbers. `FlagMinimized`/`FlagHidden` come from AX rather than a guess, and
        each app element has a 0.25 s messaging timeout so a wedged app is skipped, not waited on.
  - [x] **P2.3b** AX observer registration; callbacks enqueue and return, nothing else. **Done (D27):**
        one `AXObserver` per regular application for the five window notifications, on a run loop the
        shim owns; `observe_callback` is one line into Go. `cmd/gotab -watch` rescans on events now, a
        slow ticker demoted to a backstop. **`assumption`:** `NSWorkspace` launch/quit notifications
        need a running main run loop, which `cmd/gotab` has none of until Phase 3, so an app launched
        after gotab is unseen until the backstop sweep; and a grant revoked mid-run is untested →
        **V6.9**.
  - [x] **P2.3c** Join the two enumerations on `CGWindowID` — **done (D21): 58 candidates + 5 AX → 7
        switchable, with both of D20's misses recovered and 0 titled windows excluded.** The rule is
        `activation policy is not prohibited` AND `has a title`; policy alone admitted 37 untitled
        auxiliary windows, title alone would admit an XPC service. Two crossings, not 2N. **It costs a
        TCC grant:** CoreGraphics titles need Screen Recording, so without it the recovery branch is
        inert and the list degrades to the AX set — reported by `MissingRecovery`, not hidden. The lead
        that would remove that dependency (`kCGWindowBounds` needs no grant) is recorded in D21.
- [x] **P2.4** SkyLight/CGS Space query. **Done (D24), and it did not uphold D20's guess:** on a
      one-Space machine both windows AX could not see sat on the *current* Space, so "it must be on
      another Space" is not a general rule. `CurrentSpace()` / `SpacesOf()` / `Spaces()` populate
      `core.SpaceID` in one crossing; a bogus id returns 0, not a fabricated Space. SkyLight is
      `dlopen`/`dlsym`, so no build change. **`assumption`:** the converse — a window genuinely on a
      second Space reporting a different `SpaceID` — needs a second Space and therefore a human →
      **V6.2**.
- [x] **P2.7** `internal/app` — the event loop. **Done (D22).** Listed out of order because it was
      missing from the roadmap entirely: `ARCHITECTURE.md` has had `internal/app` in its diagram from
      the start and no task ever built it, which P2.3b surfaced by having nowhere to enqueue to. One
      goroutine owns `Model`, `Order` and `Selection`; no mutex in the package. `Post` never blocks and
      a full queue drops; `Rescan` is a depth-1 latch where dropping is *correct*. Re-enumeration
      preserves MRU by upserting with a zero `FocusSeq`. Observable with `gotab -watch`.
- [x] **P2.5** Focus / raise / minimize / close actions. **Done (D25):** `Raise` / `Minimize` /
      `Unminimize` / `Close` resolve a `CGWindowID` to an `AXUIElement` via the owning pid, with a scan
      fallback for windows only AX knows (D20). A stale id returns `ErrNoWindow` (new status
      `GT_ERR_NO_WINDOW = 6`, extending the frozen enum from `action.h`), never a panic. `Loop.Activate`
      still only changes state — putting the raise behind it is Phase 3's. **`assumption`:** the
      `ErrNotTrusted` and no-close-button paths are written but unrun → **V6.5**.
- [x] **P2.6** Thumbnail capture with explicit C-side lifecycle — wired to P1.7's policy. **Done
      (D26):** `Capture(id, maxWidth)` downscales in `SCStreamConfiguration` at capture time, is
      2 s-time-bounded, and returns an `ImageRef` the caller must `Release`. 193 ms cold / 57 ms warm
      confirms D12; capture stays off the summon path. The integrator added `gt_image_adopt` to the
      frozen `shim.{h,m}` so the live counter has a producer — `LiveImages()` returns to 0, not −1 —
      and `scripts/build.sh` weak-links ScreenCaptureKit (arrives 12.3, floor is 12.0) so the bundle
      still loads on 12.0–12.2. **`assumption`s:** a 50-window Retina cache staying inside a sane bound
      (D14 saw 8.3 MB for 20 tiles at 400 px — shape, not the number), and the 2 s capture timeout
      being headroom not a measured tail → **V6.4**.

---

## Phase 3 — UI (complete; on-screen verification is Phase 6's)

Written down as serial, then carved like Phase 2: the integrator froze
`internal/platform/darwin/panel.h` and shipped a compiling skeleton (`panel.m`, `panel.go`,
`internal/core/layout.go`), so the four tasks ran in parallel — one file pair each — against that
header. P3.3 and P3.4 branched from a main where `panel.m` was still a skeleton; the real P3.1 landed
under them at integration. See PARALLEL-WORK.md for the ownership table.

**All five tasks are done (D28–D33).** `gotab -switch` is the switcher: an ⌥⇥ event tap summons the
panel, ⌥ held with ⇥ cycles, releasing ⌥ raises the selection, Esc dismisses — behind it the loop
enumerates, orders, lays out, draws, prefetches thumbnails and restyles with the appearance, on a live
AppKit run loop. What is **not** done is seeing any of it on a real screen: an agent/CI host has no
window server, so pixels and the granted hotkey round trip are verified only in Phase 6 (V6.1–V6.5).

- [x] **P3.1** Panel + **single-view renderer** — `panel.m` / `panel.go` (D29). One flipped
      `GTTileView` draws every tile in one `drawRect:`; thumbnails ride in dumb `CALayer` sublayers
      that never call back. Placeholder art for a not-yet-captured tile (D12). Show vs update is a
      real split (D13). Verified by in-process offscreen render; the window-server screenshot is
      V6.2/V6.3.
- [x] **P3.2** Tile layout engine — `internal/core/layout.go` (D28). Pure Go, integer arithmetic:
      wraps to rows, clamps to a min/max tile size, scales the margin to the display, deterministic,
      0 allocs/op with a presized buffer. Contract extended additively; `api.go` untouched.
- [x] **P3.3** Thumbnail rendering via CALayer `contents` — `thumbnail.{h,m,go}` (D30). One capture
      goroutine (D26: SCK serialises), driven by `core.Cache` + `Capture`, setting each tile layer's
      `contents` through `OnMain`. Holds every `ImageRef` and releases it on eviction or `Stop`. The
      thumbnail-actually-appears check is V6.4 (needs the Screen Recording grant).
- [x] **P3.4** Theme / appearance / dark mode — `theme.{h,m,go}` (D31). Reads the effective
      appearance (`-bestMatchFromAppearancesWithNames:`, not `AppleInterfaceStyle`), pushes a
      `gt_palette` + HUD material through P3.1's frozen setters, and restyles on a KVO flip. The
      integrator must call `ApplyTheme()` right after `CreatePanel()` — `runSwitcher` does.
- [x] **P3.5** Hotkey → loop — `hotkey.{h,m,go}` + the tap wiring in `cmd/gotab` (D33). A session
      `CGEventTap` on its own thread recognises ⌥+Tab / ⌥+⇧+Tab / Option-release / Esc, swallows the
      switcher's chord and its keyup, ignores autorepeat, and re-enables on
      `kCGEventTapDisabledByTimeout`. The tap thread decides summon-vs-cycle, so `postGesture` is a
      stateless switch into `Loop.Post`; `Loop.Activate` raises. Without the grant `StartHotkey`
      returns `ErrNotTrusted` and `-switch` falls back to the scripted demo.
      **`assumption`:** callback latency < 5 ms, and the first crossing on the tap thread → **V6.1**;
      the granted round trip → **V6.5**.

**`assumption` across Phase 3:** that the panel behaves over a full-screen app and across Spaces. The
`collectionBehavior` flags are prior art, not a measurement (D13), and full-screen is where
switchers most often fail → **V6.2**. And that `panelRenderer.onState`'s per-state-change allocation
is acceptable — `core.Layout` is 0-alloc but the bridge builds two fresh slices → **V6.8**.

---

## Phase 4 — Product (complete; V6.7/V6.9 verify on a machine)

- [x] **P4.1** Preferences — plist under the new bundle ID (D34). `internal/prefs` is the pure-Go
      schema + defaults + `Load`/`Save`/`Set`; `darwin.Prefs` backs it with `CFPreferences` on the
      `app.gotab` domain (interops with `defaults(1)`). `gotab -prefs [Key=Value…]` reads and writes
      it; `-switch` reads the tile layout, the thumbnail cache bound, and the appearance from it.
      (The filter fields and the hotkey chord were deferred here and are now wired — P4.4 and P4.2.)
- [x] **P4.2** Settings UI (D35). `internal/platform/darwin/settings.{h,m,go}` build a native window
      over the same CFPreferences domain — 4 filter checkboxes, a blocked-apps field, an Appearance
      popup, Columns / Thumbnail-cache steppers, and a hotkey recorder. Every control change is one
      `"Key=Value"` through `prefs.Set` → `Save`. `gt_hotkey_start` now takes a chord, so `HotkeyKeyCode`
      / `HotkeyModifiers` are live end to end. `gotab -settings` opens it. (The filter checkboxes
      `write` here and the loop now `reads` them — P4.4.)
- [x] **P4.3** Permissions onboarding — Accessibility + Screen Recording (D36).
      `internal/platform/darwin/permissions.{h,m,go}`: a modal `NSAlert` naming the missing grant and
      what it is for, opening the right Privacy pane. `gotab -permissions` and `-switch` both gate on
      Accessibility, then **poll** `CheckPermissions` until the grant appears — recovery with no
      relaunch. From a shell (`stderrIsTTY`) it prints the deep links instead of the modal; with no
      window server the modal is skipped entirely. `-check` prints the `x-apple.systempreferences:`
      links. **The modal and the revoke→grant→recover loop need a human → V6.9.**
- [x] **P4.4** Multi-monitor & Spaces (D37). Two connections the switcher had the parts for but never
      made: **the filter is live** — `Loop.Rules` runs every window through `core.Filter` →
      `Order.RebuildFrom` each rescan, so `ShowMinimized` / `ShowHidden` / `ShowOtherSpace` /
      `BlockedApps` from the settings window take effect; and **the panel is sized for the display
      under the mouse** (`darwin.ActiveScreen()` → `core.LayoutOpts.Screen`/`Scale`), retiring P4.1's
      hard-coded `Scale: 2`. `Enumerate` fills `core.Window.Space`; `doRescan` refreshes
      `Rules.CurrentSpace`. **Still inert:** `ActiveAppOnly` (needs the frontmost pid on `Summon`) and
      the panel's own across-Spaces behaviour (P3.1's `collectionBehavior` → V6.2).
- [x] **P4.5** `.app` bundle packaging, ad-hoc codesign, `install.sh` (D38). `build.sh` writes a full
      Info.plist (`plutil -lint` clean, `LSUIElement`, split `CFBundleShortVersionString` /
      `CFBundleVersion`), signs the *bundle* ad-hoc and verifies it. `install.sh` / `uninstall.sh`
      honour `GOTAB_APPS`, verify the installed signature, and `--purge` also `tccutil reset`s the
      grants — round-tripped against a temp dir. `cmd/gotab` with no flags runs the switcher from
      inside `.app`, prints usage from a shell. The weak-SCK link landed earlier (`27cd840`).
      **`assumption`:** a `minos 12.0` binary launching on macOS 12, and a first launch from
      `/Applications` on a clean account (Gatekeeper on a quarantined copy) → **V6.7**.

---

## Phase 5 — Polish (complete; V6.10/V6.11 verify with a human / a host)

Carved for a 3-way fan-out (like Phases 2 and 3): `internal/i18n` and
`internal/update/update.go` are frozen API, `scripts/build.sh` and `cmd/gotab` are the integrator's,
and each task below owns a disjoint file set. See `docs/tasks/P5.{1,2,3}.md`.

**All three landed (D39–D41).** The permissions onboarding alert and its CLI lines resolve through a
two-runtime string catalog with a `de` proof; the switcher panel and the settings window expose
themselves to a screen reader; `gotab -check-update` really fetches and compares a release manifest.
None of it has been *heard* by VoiceOver or run against a live update host — those are V6.10 / V6.11.

- [x] **P5.1** Localization scaffold — `feat/P5.1` — `internal/i18n` (pure Go, embedded `en`,
      `<locale>.lproj/gotab.json` overrides) proven end to end on the permissions dialog:
      `permissions.m` draws its NSAlert through `NSLocalizedString`, and a `de` overlay
      (`Localizable.strings` + `gotab.json`) exercises both loaders. CLI usage / settings / panel
      strings are a later pass (D40).
- [x] **P5.2** VoiceOver / accessibility — `feat/P5.2` — `panel.m`'s content view is an accessibility
      container exposing one `NSAccessibilityElement` per tile (title + app label, button role,
      flipped-space frame, selected bit) and posting a high-priority announcement + a selection
      notification on each cycle; `settings.m` labels the steppers, the Appearance popup, the
      blocked-apps field and the hotkey recorder. Press is a no-op — `panel.h` exposes no raise
      callback and is frozen; activation still travels the modifier-release path.
      **`assumption`:** written against the AppKit API, never heard by a screen reader → **V6.10**.
- [x] **P5.3** Update mechanism — `feat/P5.3` — **plain version check, pure Go, stdlib only, no
      Sparkle (D39).** `internal/update.Check` GETs `FeedURL` (5 s-bounded, 64 KiB cap), decodes the
      JSON manifest (`manifest.go`), and compares the numeric version triple (`version.go`);
      `gotab -check-update` is the entry point. `min_macos` is decoded but not yet enforced. No
      download / install (needs notarization the ad-hoc bundle lacks — D38).
      **`assumption`:** only run against a local manifest, `FeedURL` has no host → **V6.11**.

---

## Phase 6 — Verification

Everything Phases 0–5 deferred, run once against the assembled app. **This phase is not optional and it
is not a formality**: it is where the `assumption` tags above are cashed in, and a task here coming back
negative is expected to send work back into an earlier phase rather than be waved through.

Serial, one owner. Of what remains, V6.2 / V6.9 / V6.10 need a human at the machine and V6.3 / V6.4 /
V6.5 / V6.7 need a real Mac with a window server — TCC blocks synthesising the input and an agent host
has no display, the same wall P0.7 and P0.1 hit, not a gap in the tooling.

**Order matters.** V6.1 (done) and V6.2 are Phase 0/3 debts — run first, because either coming back
badly changes Phase 2/3 code rather than merely reporting on it. V6.1 came back clean (D45).

**Done so far:** V6.6, V6.8, V6.11, V6.12 (agent-run, 2026-09-08). **V6.1 (2026-09-08, D45)** — the
first task of the on-machine session. What is left: V6.2–V6.5, V6.7, V6.9, V6.10 — all have a turnkey
`docs/tasks/V6.N.md`; `☐ ready` = contract written, needs the machine/human step.

| ID | status | what | acceptance | needs |
|---|---|---|---|---|
| **V6.1** | ✅ **done** | Hotkey delivery latency, real keypress | **3.2 ms worst case over 20 real ⌥⇥ presses** (mean 0.6, p50 0.28), 64 % of the 5 ms budget; first C→Go crossing 28 µs and not an outlier; no `kCGEventTapDisabledByTimeout`. D45; P0.2 → `[x]`. Contract: `docs/tasks/V6.1.md` | — (done) |
| **V6.2** | ☐ ready | Panel over a full-screen app and across Spaces | panel appears **over** a full-screen app and follows the user across Spaces, verified by screenshot rather than by the absence of an error; plus P2.4's SpaceID converse. Contract: `docs/tasks/V6.2.md` | a human, ≥ 2 Spaces |
| **V6.3** | ☐ ready | Summon → pixels, end to end | **< 100 ms** on the real app, timed to the CA commit and not to the call returning (D13: timing the call is wrong by 10x). Contract: `docs/tasks/V6.3.md` | a Mac with a window server |
| **V6.4** | ☐ ready | Thumbnail memory at realistic scale | 50-window cache at Retina resolution stays inside the stated bound; measured on `spike/procmem`'s **`IOSurface`** row, sampled at summon (D8: the resident figure decays within seconds). Contract: `docs/tasks/V6.4.md` | a Mac, Screen Recording granted |
| **V6.5** | ⚠️ **partial → Phase 7** | `internal/platform` behaviour | Driven on a real machine (2026-09-08, D46). **Enumeration ✓** — join, `cg`-recovery and exclusion-with-reason all correct. **Raise ✓ for AX-visible windows** (`both` / `ax` origin), **✗ for `cg`-only windows**: every `cg`-only raise returns `ErrNoWindow` because `gt_window_raise` resolves through `kAXWindowsAttribute`, which does not list them. The switcher shows tiles it cannot action — sent to **Phase 7**. Minimized / hidden-app / the failure sentinels: still to run. Contract: `docs/tasks/V6.5.md` | a Mac, both grants |
| **V6.6** | ✅ **done** | Deferred table tests for the pure surfaces Phases 2–5 grew | `internal/prefs`, `internal/i18n`, `internal/update` (`version`/`manifest`/`check` over `httptest`), the event loop's `handle`, and `HotkeyDisplay` — `internal/core`-style table tests; `scripts/check.sh` green. Contract: `docs/tasks/V6.6.md` | — |
| **V6.7** | ☐ ready | Ship the bundle (P4.5) | self-verified half done (D38). Machine half: `build/GoTab.app` launches from `/Applications` on a genuinely clean account (Gatekeeper needs a right-click → Open on a quarantined ad-hoc copy), and on a real macOS 12.0–12.2 host (`minos`/plist *agree*; a 12.0 binary *running* on 12.0 is untested — D17). Contract: `docs/tasks/V6.7.md` | a clean account + a macOS 12 host |
| **V6.8** | ✅ **done** | No allocation on the hot path | `go test ./internal/core/... -bench . -benchmem` = **0 allocs/op** for the summon/cycle/dismiss kernels, plus a new `internal/app` `BenchmarkHandleGesture` (0 allocs/op) proving the loop's real gesture path is clean, and the `panelRenderer.onState` allocation measured + accepted (**D44**). Contract: `docs/tasks/V6.8.md` | — |
| **V6.9** | ☐ ready | Permissions onboarding (P4.3) | from a **revoked** state, `-switch` shows the alert, and granting in Settings brings the switcher up **without a relaunch** — the 750 ms poll picks it up. Both grants, plus a mid-run revoke. The headless fallback and the poll are already exercised (D36); this is the modal + the human loop. Contract: `docs/tasks/V6.9.md` | a human |
| **V6.10** | ☐ ready | VoiceOver heard, not just written (P5.2) | with VoiceOver on, driving `gotab -switch`: each tile announced as "&lt;title&gt;, &lt;app&gt;", the selection spoken on **every** ⌥⇥ cycle, the panel a container labelled "Window switcher"; and every `gotab -settings` control labelled. An agent host has no screen reader (D41). Contract: `docs/tasks/V6.10.md` | a human |
| **V6.11** | ✅ **done** | Update check against a real host (P5.3) | `gotab -check-update` against the **published** `FeedURL` — advertised-higher prints "available" + notes, equal/older prints "up to date", unreachable prints the one-line warning and exits 0. All three run 2026-09-08 against the live feed (then advertising `0.2.1`); `httptest` regression coverage added under V6.6. Contract: `docs/tasks/V6.11.md` | — |
| **V6.12** | ✅ **done** | Release automation (CI/CD) (D42) | pushing a `v*` tag runs the gate, builds `GoTab.app` with the tag as `CFBundleShortVersionString`, and cuts a GitHub Release carrying `GoTab-<tag>.zip` + its `.sha256`; `latest.json` at the Pages URL then advertises that version. Ran end to end for `v0.2.0` **and** `v0.2.1` (D42; `docs/tasks/V6.12.md` Findings). Unblocked V6.11 | — |

**A task contract goes in `docs/tasks/V6.N.md` before that task starts**, same as every other numbered
task. All twelve now exist: V6.6 / V6.8 / V6.11 got theirs as they were run, V6.12's predates it, and
V6.1–V6.5 / V6.7 / V6.9 / V6.10 were written up front (2026-09-08) from the roadmap rows and the
`assumption` tags so the Mac session is execute-only. Each names the exact protocol, the acceptance
bar, and — the point of the phase — where a negative result sends the work back.

---

## Phase 7 — Actionability: every tile can be switched to, or is not a tile

**Why this phase exists:** V6.5 (D46) drove the assembled switcher and found the gap the P2.3c join
(D21) always implied. The join recovers windows Accessibility cannot enumerate — usually on another
Space — so the panel shows them; but `gt_window_raise` resolves a window through
`kAXWindowsAttribute`, which does not list them, so selecting one returns `ErrNoWindow` and nothing
happens. A window closed with ⌘W while its app stays alive can linger the same way. **A tile that
does nothing is worse than no tile.**

Two directions, and this phase does both: reach the window's *application* when the window itself is
out of AX's reach, and drop a window from the list once it is genuinely gone.

**Testing stays deferred (D16 / D23):** Phase 7's tasks carry acceptance criteria but no per-task
tests; their on-machine verification is new rows in [Phase 6](#phase-6--verification)'s table
(V6.13, V6.14).

- [x] **P7.1** Raise falls back to the application — `feat/P7.1` — **code landed.** When
      `gt_window_raise` cannot resolve a window id to an `AXUIElement` (status `GT_ERR_NO_WINDOW`)
      **and the owning process is a running application**, it now calls `activate_app(pid)` and
      returns `GT_OK` rather than `GT_ERR_NO_WINDOW`. `Loop.Activate` already treats a nil error as
      "switched"; the specific window is not ordered, but the app the user was reaching for comes
      forward. `copy_window_element` hands back the owning pid even on an empty return so the fallback
      has a target; `app_is_alive` (via `runningApplicationWithProcessIdentifier:`) is the line
      between "activate the app" and `ErrNoWindow`. A window whose owning pid is **gone** still
      returns `ErrNoWindow` (P7.2 removes it); `GT_ERR_TIMEOUT` / `GT_ERR_NOT_TRUSTED` are returned
      as-is, not rerouted. Contract: `docs/tasks/P7.1.md`. Gate green, no tests added (D16 / D23).
      **`assumption` → V6.13:** that the app-activation fallback reaches the intended app for a
      `cg`-only window and that nil is the right contract for `Loop.Activate` — the `cg`-only path is
      exercised only on a machine. **Acceptance (V6.13):** in `gotab -switch`, selecting a window that
      `spike/raisetest` reports un-resolvable brings its application frontmost; over 10 such
      selections `OnError` logs zero `ErrNoWindow` for a window whose pid is alive.
- [ ] **P7.2** Dead windows leave the list — `feat/P7.2` — a window is removed from the model when
      its owning pid is no longer running, or when enumeration has not returned it for one full
      rescan (it already is — confirm the removal path fires) . Add a pid-liveness check to
      `doRescan`'s prune so a ⌘W'd window of a still-running app cannot survive on a stale
      CoreGraphics entry. **Acceptance:** a window closed with ⌘W (its app still running) is absent
      from `gotab -list` and from the panel within one rescan; a window whose process has exited is
      absent within one rescan; `gotab -watch` shows the removal.
- [ ] **P7.3** *(optional)* Space-aware raise — `feat/P7.3` — raise a window that is genuinely on
      another Space by switching to that Space first (SkyLight: `CGSManagedDisplaySetCurrentSpace` or
      the documented-enough equivalent), so P7.1's app-only fallback becomes the exception. **This is
      a private-API bet and may not survive a macOS release** — it is optional, and P7.1 is the
      floor. **Acceptance:** with two Spaces, selecting a window on the other Space switches to that
      Space and brings that exact window forward; verified by a human alongside V6.2.
- [ ] **P7.4** Decide and document the default — `feat/P7.4` — with P7.1 in place an un-raiseable
      window is still useful (it reaches the app), so the default is to **keep** showing it; P7.2
      only removes windows that are actually gone. Record this in `docs/ARCHITECTURE.md` next to the
      enumeration rule, and add a `prefs` field only if hiding app-only-reachable windows turns out
      to be wanted. **Acceptance:** `ARCHITECTURE.md` states the rule; `scripts/check.sh` green.

**Phase 7 verification (rows in Phase 6's table):**

| ID | what | acceptance | needs |
|---|---|---|---|
| **V6.13** | P7.1 raise-fallback on a real machine | selecting a `cg`-only tile in `gotab -switch` brings its app frontmost; no `ErrNoWindow` for a live-pid window over 10 trials, incl. minimized and hidden-app targets | a Mac, both grants |
| **V6.14** | P7.2 dead-window pruning | ⌘W a window (app alive) → gone from `-list`/panel within one rescan; kill an app → its windows gone within one rescan | a Mac, both grants |

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
- `2026-09-06` — Handoff. Added `docs/PLATFORM-LESSONS.md` (macOS window-switcher platform knowledge,
  distilled from prior art), D6 (project framing and rejected alternatives) and D7. Fixed a broken
  build (`cmd/gotab` was empty) and a false-positive purity gate. Added P0.7: **the reference
  switcher's memory was never measured, so the then-primary goal has no baseline.**
  **Next session: start with P0.4a (`docs/tasks/P0.4a.md`), then P0.7.** Those two together decide whether
  the memory premise holds; everything else is downstream of that answer.
- `2026-09-06` — **P0.4a done.** Built `spike/memprobe`, which tested three hypotheses and rejected all of
  them before finding the real answer: `vmmap` has a dedicated `CG raster data` region that accounts our
  bitmaps exactly (368.8 MB of 366.2 MB declared), and `Physical footprint (peak)` caught the true
  374.4 MB high-water mark. Instantaneous `phys_footprint` misses it because macOS reclaims idle CG raster
  pages. Corrected D4 in D8.
  **D9 is the uncomfortable part: macOS already evicts idle thumbnails, so the bounded-LRU win may be
  small.** That makes P0.7 (measure a reference switcher) the deciding task — do it before designing the cache.
  **Next session: P0.7.**
- `2026-09-06` — Docs/tooling repair, no code change. The repo's only branch is `main`, but `scripts/wt.sh
  done` ran `git checkout master`, CI's push trigger watched `master`, and `PARALLEL-WORK.md` said the
  same — the first merged worktree would have failed. All switched to `main`. Also documented how to run a
  spike (`go run ./spike/<name>`), why `check.sh` omits `set -e` (all four steps report, not just the
  first), and noted in `ARCHITECTURE.md` that its four-layer diagram is the target, not the current tree —
  only `cmd/gotab` and `spike/` exist today. Fixed two bugs in `wt.sh`'s usage output while there: the
  line range leaked `set -euo pipefail`, and `sed 's/^# \?//'` is a GNU-ism that BSD sed reads as a
  literal `?`, so it never stripped the comment prefixes on the one platform this project supports.
  **Next session: still P0.7 (measure a reference switcher).**
- `2026-09-06` — **P0.7 started, not finished.** Built `spike/procmem`: D8's three quantities
  (`CG raster data`, footprint, peak) read out of `vmmap --summary` for *any* pid, since `spike/memprobe`
  can only measure itself. Validated against a holder process with a known 366.2 MB — reports 368.8 MB
  virtual, matching D8 exactly. Captured a reference switcher's idle baseline: **27.4 MB
  footprint, 28.4 MB peak, and no `CG raster data` region at all** (it had never been summoned).
  Two things learned that P0.7's contract now records: `CG raster` RESIDENT decays fast — the same
  366 MB reads 368.8 MB when sampled immediately after a touch and **6.4 MB** ~15 s later — so summon-time
  sampling is mandatory and `Physical footprint (peak)` is the only non-decaying number worth quoting.
  And that switcher relaunches itself under a new pid on permission grant, which silently killed the
  first 90-sample run; `-name` now re-resolves every sample.
  **Next session: the measurement still needs a human** to grant Accessibility + Screen Recording and
  press ⌥⇥ (TCC blocks synthetic keystrokes). Protocol is in `docs/tasks/P0.7.md`.
- `2026-09-06` — **Direction change: memory parity with another switcher is no longer a goal (D10).** The
  project is now feature parity with the core switching such a tool is expected to do, written in Go, on a
  sane memory budget. P0.7 dropped, Phase 0 reframed from a go/no-go gate to de-risking the three platform
  unknowns (panel, hotkey, ScreenCaptureKit), and the comparative gate criterion replaced with a leak
  check and a bounded-cache requirement. `spike/procmem` survives as the general memory instrument for any pid.
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
  `docs/PLATFORM-LESSONS.md` cited prior art at a personal absolute path; that citation is gone and the
  file stands on its own. Worktrees moved from the sibling `../gotab-wt/` into `.worktrees/`
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
  cold frame does not commit for ~14 ms, exactly as PLATFORM-LESSONS predicted, so timing the call would
  have been wrong by 10x — the spike reports three timestamps instead. And `drawRect:` is instrumented
  to prove the warm number is a real re-render: 0 of 30 warm summons skipped the draw. The
  non-activating combination works — the frontmost app's pid never changed across 20 summons.
  **One thing is deliberately not claimed:** behaviour over a full-screen app and across Spaces. The
  `collectionBehavior` flags are prior art, not a measurement, and TCC blocks driving either
  synthetically. That needs a human and is the open half of P0.1.
  **Next session: P0.2 (CGEventTap hotkey), the last Phase 0 unknown**, then P0.5 closes the phase and
  Phase 2 begins — serial, one owner.
- `2026-09-06` — **Phase 0 closed, and testing moved to the end (D15, D16).** P0.4b ticked on D14 (100
  capture/release cycles, `IOSurface` +0.0 MB, with a held-20-thumbnail control proving the instrument
  is not merely blind). P0.5 written up as D15. **P0.2 is deliberately left `[~]`, not `[x]`** — the
  hotkey spike is complete code with no number behind it, because only a real keypress measures the
  thing the 5 ms budget is about and TCC will not let one be synthesised.
  The direction change: per-task tests and measurements are **deferred to the new Phase 6**, so Phases
  2–5 are done when the code is written and the gate is still green. The four decisions that now rest
  on unverified assumptions are tagged **`assumption`** in place and each points at the V6 task that
  settles it, so the debt is visible from the roadmap rather than remembered. Phase 1's existing tests
  stay and stay green — deferring means writing no new ones, not deleting the suite.
  **Next session: P2.1 (C shim skeleton + cgo build integration). Phase 2 does NOT fan out** — one
  owner, sequential, shared C shim and main thread.
- `2026-09-06` — **P2.1 done. Phase 2 opens by finding a bug in the shipping bundle.** The shim skeleton
  is the small part: `internal/platform/darwin` now compiles ObjC and links, `gt_init` carries D12's
  `CGMainDisplayID()` trap where the abort can't surprise a later task, and `shim.h` fixes the
  conventions later tasks follow instead of each inventing one. `gotab -check` reports both TCC grants.
  The find is D17, in two parts. Go 1.26's floor is **`minos 12.0`, not the 11.0 D2 recorded** — the
  toolchain moved under a number written down once and treated as permanent, and README, ARCHITECTURE,
  `install.sh` and `Info.plist` had all been repeating it. Worse: **once a real `.m` file exists, clang
  sets the deployment target, not the Go linker**, and clang defaults to the SDK's version. The `.app`
  declared `LSMinimumSystemVersion 11.0` and carried `minos 26.0`. It builds, signs and runs perfectly
  on the machine that built it, and refuses to launch anywhere older than macOS 26 — a failure that only
  ever appears on someone else's computer. `build.sh` now derives the plist and the Mach-O stamp from
  one `MIN_MACOS` variable and **fails** if any slice disagrees; the check matters more than the fix,
  because a new framework or an Xcode bump puts it back one line at a time.
  **Next session: P2.2 (batched window query — one call, one packed array).** Still serial, one owner.
- `2026-09-06` — **`cmd/gotab` had never been committed (D18).** Noticed because `git status` did not
  list a file P2.1 had just rewritten. `.gitignore`'s unanchored `gotab` — meant for the built binary in
  the repo root — matched the `cmd/gotab/` **directory**, so the project's only `package main` was
  absent from every clone and `scripts/build.sh` has been failing in CI since the first commit. Locally
  nothing showed it: the file is on disk, the gate is green, the `.app` builds. An earlier session saw
  the same symptom, read it as "cmd/gotab was empty", and fixed the contents instead of the tracking.
  Patterns anchored (`/gotab`, `/spike/spike`) and the package committed. The gate could not have caught
  this — it checks the working tree, not what a clone would contain — so it stays V6.7's job, which now
  has a real failure to catch rather than a hypothetical one.
- `2026-09-06` — **P2.2 done, and it says something uncomfortable about P2.3.** One crossing returns
  every window as a packed array of fixed-size records; the crossing count does not grow with N, which
  is the cgo rule made concrete. Two design points worth keeping: there are **no flag bits in C** —
  `core.WindowFlags` is frozen, a second copy in C is exactly the drift AGENTS.md warns about, so C
  reports facts (layer, alpha, on-screen) and Go composes the flags — and titles use
  `CFStringGetBytes` rather than `CFStringGetCString`, so an over-long title truncates on a character
  boundary instead of failing outright or emitting invalid UTF-8. Window titles are full of emoji and
  CJK; this is the common case.
  **D19 is the find.** On a normal session `CGWindowList` returns 82 windows, 59 after dropping
  `kCGWindowLayer != 0`, of which **7** are things a user could switch to. The titled seven were exactly
  right — but the title is not a filter either: it is empty for everything without Screen Recording, and
  a real untitled document window exists. So **P2.3 owns enumeration, not just observation**, which is
  why a switcher is built on Accessibility. P2.2's list becomes the candidate set AX filters. The batching
  discipline is what keeps an 8x over-count cheap: 59 records still cost one crossing.
  **Next session: P2.3 (AX enumeration + observers).** Still serial, one owner.
- `2026-09-06` — **P2.3a done, and it found the thing that decides Phase 2's shape (D20).** The
  Accessibility enumeration works and is dramatic: **5 switchable windows against CoreGraphics' 59
  candidates**, with all 5 carrying an ID CoreGraphics also reports — which is the evidence that
  matters, because the AX list only has a `CGWindowID` at all through the private
  `_AXUIElementGetWindow`, and 5 of 5 agreeing with a public API says it returns real numbers.
  **Then the same run showed AX missing two windows CoreGraphics could see.** A second Chrome window
  (`kAXWindowsAttribute` returned 1 for a process that has 2) and an `.Accessory` app's perfectly
  ordinary titled window. Probed both directly rather than guessing: **both AX calls succeeded** — no
  error, no timeout, no missing grant. The answers were simply short.
  So **neither enumeration is correct alone**: CoreGraphics sees every window and cannot say which are
  switchable, AX says which are switchable and cannot see every window. The model must be built from a
  join on `CGWindowID`, which is now **P2.3c** and is not optional — shipping P2.3a's list alone is a
  switcher that silently cannot reach a window on another Space. **P2.4 is promoted** to the task that
  confirms *why*, since "it is on another Space" is currently the likeliest story and not a measurement;
  driving a Space change needs a human, so V6.2 carries the human half.
  **Next session: P2.3c (the join), then `internal/app` — P2.3b's callbacks have nowhere to enqueue to
  until the event loop exists.** Still serial, one owner.
- `2026-09-06` — **P2.3c done: the join produces the right list (D21).** 58 CoreGraphics candidates and
  5 Accessibility windows join to **7 switchable windows** — the five AX saw plus both windows D20 found
  it missing — with 51 exclusions each carrying a named reason and **no titled window excluded**, which
  is the task's contract discharged rather than asserted. Two crossings, not 2N.
  **The admission rule needed both halves, and finding that out cost a wrong first attempt.** Activation
  policy alone produced a 44-window list: real applications own a great many untitled layer-0 windows —
  offscreen buffers, popovers, toolbars — that nobody can switch to. Title alone would admit any XPC
  service that has one. Together they give exactly 7.
  **The catch is a TCC dependency.** `kCGWindowName` needs Screen Recording, so without that grant the
  CoreGraphics-only recovery admits nothing and the list quietly degrades to the AX set — losing the
  other-Space windows the join exists to recover. It is reported (`MissingRecovery`) rather than hidden,
  and it means the Screen Recording grant is not only about thumbnails, which P4.3's onboarding copy
  should say. D21 records the lead that would remove the dependency: `kCGWindowBounds` needs no grant,
  and the 37 wrongly-admitted windows are plausibly separable by size. That is a measurement, not a
  guess, and it was not made here.
  **Next session: `internal/app` — the event loop.** P2.3b's observers have had nowhere to enqueue to
  since D20, and it is now the only thing between here and a switcher that reacts to anything.
- `2026-09-06` — **P2.7 done: `internal/app` exists, and it was never on the roadmap.** ARCHITECTURE has
  had it in the four-layer diagram since the project started and no task ever built it; P2.3b surfaced
  the omission by having nowhere to enqueue to. One goroutine owns `Model`, `Order` and `Selection`, and
  there is no mutex in the package — if a second goroutine ever needs the model the fix is to move that
  caller onto the loop, not to lock it.
  **D22 is the design decision.** A blocking channel send inside a platform callback is a bug with a
  name: the system disables a tap whose callback is too slow, which is the failure mode P0.2's spike
  already handles explicitly. So `Post` is non-blocking always, and that forces a decision about a full
  queue that differs by event. Discrete events get a depth-256 buffer and a drop is reported; "the window
  set changed" gets a **depth-1 latch where dropping is correct**, because the pending signal already
  means re-read everything — twenty notifications during a Space switch coalesce into one enumeration.
  **MRU preservation turned out to be an argument value, not a branch.** Rescan touches every window
  every pass and upserts with a zero `FocusSeq`, which `Model.Upsert` reads as "preserve". Observed 12
  rescans over 6 seconds producing one state print — the order did not churn. That shows enumeration is
  stable; it does not exercise a title changing mid-run, which core already pins in
  `TestModelUpsertZeroFocusPreserves`.
  **Next session: P2.3b (AX observers, now unblocked), then P2.5 (raise).** P2.5 is the one that makes
  the app do something: `Loop.Activate` is a state change with nothing behind it until then.
- `2026-09-07` — **Phase 2 closed. The fan-out's four tasks are all landed (D24–D27).** P2.4, P2.5 and
  P2.6 had merged their code earlier but left `ROADMAP.md` and `DECISIONS.md` to the integrator (the
  fan-out rule in PARALLEL-WORK.md); this session consolidated them and integrated P2.3b, whose code
  had been sitting uncommitted in its worktree.
  **P2.4 (D24):** `core.SpaceID` is populated, and D20's guess did not survive contact — on a one-Space
  machine both windows Accessibility could not enumerate sat on the *current* Space, so "invisible to
  AX ⇒ on another Space" is false. The converse (a real second-Space window reporting a different id)
  still needs a human → V6.2. SkyLight via `dlopen`, so no build change.
  **P2.5 (D25):** raise / minimize / unminimize / close. The finding is that a switch is two
  independent halves — `kAXRaiseAction` orders the window inside its app, `activateWithOptions:` makes
  the app frontmost, and restoring from the Dock does the second without the first. `kAXRaiseAction`
  also never answers during the Dock's restore animation (250 ms timeout, action still lands), so that
  one call reads `kAXErrorCannotComplete` as success. New status `GT_ERR_NO_WINDOW = 6` extends the
  frozen enum from `action.h`.
  **P2.6 (D26):** ScreenCaptureKit capture, downscaled at capture time, 2 s-bounded, `ImageRef` the
  caller releases. 193/57 ms cold/warm confirms D12. Two integrator escalations landed: `gt_image_adopt`
  added to the frozen `shim.{h,m}` so the live counter has a producer (`LiveImages()` was going to −1),
  and `scripts/build.sh` now weak-links ScreenCaptureKit behind `-tags gotab_weak_sck` +
  `CGO_LDFLAGS_ALLOW` so the bundle still launches on macOS 12.0–12.2.
  **P2.3b (D27):** one `AXObserver` per regular app for the five window notifications, on a run loop the
  shim owns; the callback is one line into Go. `cmd/gotab -watch` now rescans on events, with a 2 s
  ticker demoted to a backstop. The catch it inherits: `NSWorkspace` launch/quit notifications are only
  delivered by a running **main run loop**, which `cmd/gotab` has none of until Phase 3 — so an app
  started after gotab is unseen until the backstop sweep. Grant-revoked-mid-run is untested → V6.9.
  **Next session: Phase 3 (UI, serial) — P3.1, the panel + single-view renderer.** No P3 task has a
  contract in `docs/tasks/` yet; write P3.1's before starting it. Phase 3 is also where `cmd/gotab`
  finally runs a main run loop, which closes P2.3b's open half.
- `2026-09-07` — **Phase 3 carved and fanned out.** Called serial by the earlier plan; carved instead,
  the same move as Phase 2's `7e7751e`. The integrator froze `internal/platform/darwin/panel.h` (the
  `gt_tile` / `gt_palette` shapes and every panel function) and shipped a compiling skeleton —
  `panel.m` creates the real NSPanel/views from `spike/panel`'s proven code and stubs the rest,
  `panel.go` does real tile marshalling, `internal/core/layout.go` is a single-row equal split. Four
  contracts written (`docs/tasks/P3.1–P3.4.md`), four worktrees, four agents in parallel: **P3.1**
  owns `panel.m`/`panel.go`, **P3.2** `core/layout.go`, **P3.3** `thumbnail.{h,m,go}`, **P3.4**
  `theme.{h,m,go}`. P3.3/P3.4 branch from a main where `panel.m` is still the skeleton and link
  against it; the real P3.1 lands under them at integration — the "heavier merge" this fan-out trades
  for wall-clock. Gate green on the skeleton.
  **Next: integrate the four branches** (P3.1 first — it is what P3.3/P3.4 assumed), then wire
  `internal/app` → `core.Layout` → `darwin.ShowPanel` and give `cmd/gotab` a real run loop.
- `2026-09-07` — **Phase 3 rendering landed and wired (D28–D32).** All four agents came back green
  against the frozen `panel.h`; the "heavier merge" the max fan-out traded for was in fact clean —
  P3.2 touched only `layout.go`, P3.3/P3.4 added new file pairs, and P3.1's real `panel.m` slid under
  them with no conflict. Merged in order P3.2 → P3.3 → P3.4 → P3.1.
  **P3.2 (D28):** `core.Layout` — wrap, min/max clamp, display-scaled margin, deterministic,
  0-alloc; contract extended by adding fields only. **P3.1 (D29):** one flipped `GTTileView` draws
  every tile, thumbnails in dumb CALayers, placeholder for a not-yet-captured tile, show/update
  split; verified by in-process offscreen render (no grant needed), window-server screenshot is
  V6.2/V6.3. **P3.3 (D30):** one-goroutine `Prefetcher` drives `core.Cache` + `Capture`, owns and
  releases every `ImageRef`; the thumbnail-appears / `LiveImages` checks need the Screen Recording
  grant → V6.4. **P3.4 (D31):** palette + HUD vibrancy from the effective appearance, KVO restyle;
  integrator calls `ApplyTheme` right after `CreatePanel`.
  **Integration (D32):** `runloop.{h,m,go}` (`CFRunLoopRun`, not `-[NSApp run]`), a `panelRenderer`
  on `Loop.OnState`, `Loop.Activate` now calls `darwin.Raise`. `gotab -switch` runs the whole
  pipeline on a live AppKit loop and scripts one summon — **P2.3b's open half closes here**, since
  the main loop now delivers `NSWorkspace` notifications. The gate gained `go build ./...` (a missing
  `-framework` slipped `vet`+`test`; P3.3/P3.4 both hit it with QuartzCore).
  **Next: P3.5 — route the ⌥⇥ hotkey (P0.2's spike) into `Loop.Post`.** That is the last thing
  between `-switch`'s scripted demo and a switcher a person can use. Then V6.2/V6.3 put it on a real
  screen.
- `2026-09-07` — **P3.5 done — the hotkey is wired, and Phase 3 is complete (D33).**
  `hotkey.{h,m,go}` install a session `CGEventTap` at the queue head on its own thread (the
  `observe.m` pattern, for the same reason: a keystroke must not queue behind a panel draw). The tap
  thread tracks the gesture — first ⌥+Tab of a hold → `SUMMON_*`, later ones → `CYCLE_*`,
  Option-release → `ACTIVATE` if armed, Esc → `DISMISS` — so `cmd/gotab`'s `postGesture` is a
  stateless switch into `Loop.Post`. ⌥+Tab and its keyup are swallowed, autorepeat is dropped,
  `kCGEventTapDisabledByTimeout` re-enables. Without the Accessibility grant `StartHotkey` returns
  `ErrNotTrusted` and `-switch` falls back to the scripted demo — smoke-tested, clean SIGINT exit.
  **The switcher now switches** (`Loop.Activate` → `darwin.Raise`), but only the no-grant path is
  reachable from here: the tap seeing a real ⌥⇥, the summon→cycle→raise round trip, and the < 5 ms
  callback budget are all **V6.1 / V6.5**, needing the grant and a human.
  **Next: Phase 4 (Product) — P4.1, the preferences plist under the new bundle ID.** Or run the
  Phase 6 debts that are now cheap: V6.1 (hotkey latency) and V6.2 (panel over full-screen / across
  Spaces) both just need a human at the machine, and either coming back badly changes Phase 3 code.
- `2026-09-07` — **P4.1 done — settings persist in the bundle-id plist (D34).** `internal/prefs` is
  the pure-Go schema (`Prefs`, `Default()`, `Load`/`Save`/`Set`, `LayoutOpts()`/`Rules()`);
  `internal/platform/darwin/prefs.{h,m,go}` back its `Reader`/`Writer` with `CFPreferencesCopyAppValue`
  / `SetAppValue` on `kCFPreferencesCurrentApplication`. From `build/GoTab.app` that domain is
  `app.gotab` and `defaults read app.gotab` shows the schema keys; under a bare `go build` it is the
  binary's own name, so a dev build cannot scribble on the real prefs (the TCC identity rule again).
  `gotab -prefs` prints the effective settings, `gotab -prefs MaxColumns=5 Appearance=dark` sets them.
  `-switch` reads `MaxColumns`/`TileWidth`/`TileHeight`, `ThumbnailCacheSize`, and `Appearance` (a
  forced Light/Dark via a new `darwin.SetAppearance` override in `theme.go`, no `theme.m` change).
  **Two schema fields are inert until a later task:** the filter (`ShowMinimized` etc.) needs
  `core.Rules` wired into `internal/app`'s loop, which never filters today — P4.4's or its own; the
  hotkey chord needs `gt_hotkey_start` to take a keycode — P4.2's (rebinding is a settings-UI
  feature). Round-trip and `defaults` interop verified; gate green, universal app builds.
  **Next: P4.2 (Settings UI), or P4.3 (permissions onboarding — the one thing every path here has
  needed a human for).** Or cash the V6.1/V6.2 Phase 0/3 debts.
- `2026-09-07` — **P4.2 done — a native settings window, and the hotkey is rebindable (D35).**
  `internal/platform/darwin/settings.{h,m,go}` build one fixed-size `NSWindow` — 4 filter checkboxes,
  a blocked-apps text field, an Appearance popup, Columns / Thumbnail-cache steppers, a hotkey
  recorder. Manual top-down frames, no Auto Layout. Every control funnels through one
  `emit(key, value)` → `goSettingsAssign("Key=Value")` → `prefs.Set` → `Save`, so there is no
  per-control marshalling and no second schema. `darwin` still does not import `internal/prefs`
  (`SettingsValues` is primitives; `darwin.Prefs` implements the interfaces structurally).
  `gt_hotkey_start` now takes `(keycode, modifiers)` — `g_chord_key`/`g_chord_mods` replace the
  hardcoded ⌥Tab, the commit edge generalises to any modifier set — so `HotkeyKeyCode` /
  `HotkeyModifiers` are live from `-prefs` and the recorder through to `-switch`'s `StartHotkey`.
  `HotkeyDisplay` (`hotkey.go`) is the one chord formatter. `gotab -settings` opens the window on its
  own run loop (Regular activation policy while open); it is its own process — a running `-switch`
  re-reads prefs only on next launch. Smoke-tested: opens, exits 0 on close/^C; the `Key=Value` path
  is verified through `-prefs`. The window's pixels and the recorder capturing a real chord are
  V6.9 (a human).
  **Next: P4.3 (permissions onboarding) — from a revoked state, explain which grant is missing and
  recover without a relaunch loop.** Then P4.4 (multi-monitor & Spaces, which also wires the filter),
  P4.5 (packaging). The V6.1/V6.2 human debts are still cheap and still worth doing first.
- `2026-09-07` — **P4.3 done — onboarding explains the missing grant and recovers by polling (D36).**
  `internal/platform/darwin/permissions.{h,m,go}`: a modal `NSAlert` names each missing grant, says
  what it is for, and on "Open System Settings" opens the pane (and `CGRequestScreenCaptureAccess` for
  the SR list row). `gotab -permissions` and `-switch` both call it, then poll `CheckPermissions`
  every 750 ms until the grant appears — no relaunch; and macOS relaunching gotab itself on the AX
  grant is fine, the fresh process passes the gate. `-switch` caps the wait at 5 min then quits with a
  re-open hint; Screen Recording missing is a one-line warning and it runs on. Two guards that were
  not optional: from a shell (`stderrIsTTY`) it prints the `x-apple.systempreferences:` deep links
  instead of a focus-stealing modal, and with no window server (`[NSScreen screens] == 0`) the modal
  is skipped so `runModal` cannot hang unkillably. `-check` gained the deep links. TTY path and the
  poll verified via a pty; the modal and the full revoke→grant→recover loop are **V6.9**.
  **Next: P4.4 (multi-monitor & Spaces) — it also wires `core.Rules` into the loop, closing P4.1/P4.2's
  open filter item. Then P4.5 (packaging, `install.sh`).** V6.1/V6.2 remain the cheap human debts.
- `2026-09-07` — **P4.4 done — the filter is live and the panel is sized for its display (D37).**
  Two long-standing disconnects closed. **Filter:** `Loop.Rules` (from `Prefs.Rules()`) runs the
  model through `core.Filter` → the new `core.Order.RebuildFrom` on every rescan, so `ShowMinimized`
  / `ShowHidden` / `ShowOtherSpace` / `BlockedApps` from the settings window take effect;
  `Enumerate` now fills `core.Window.Space` (one `SpacesOf` crossing) and `doRescan` refreshes
  `Rules.CurrentSpace`. Rebuilding every pass is safe — `RebuildFrom` sorts by `FocusSeq` only, so a
  no-op pass is byte-identical and `-watch`'s print-on-change (P2.7's MRU check) stays quiet.
  **Display:** `darwin.ActiveScreen()` (the screen under the mouse, shared with `gt_panel_show`)
  feeds `core.LayoutOpts.Screen`/`Scale`, retiring P4.1's `Scale: 2` guess. `Rebuild` and
  `RebuildFrom` share `sortByFocus`. Gate green (`internal/core` suite unbroken by the refactor);
  degraded paths (`-list -raw`, `-watch` with no grant) still fine. **Left inert:** `ActiveAppOnly`
  (needs the frontmost pid on `Summon`) and the panel's own cross-Spaces behaviour (P3.1 → V6.2).
  **Next: P4.5 — package the `.app`: `install.sh` / `uninstall.sh` round-trip, ad-hoc codesign,
  verify the universal bundle. `build.sh` already does the weak-SCK link (this session).** Then Phase
  5, or hand the V6.1/V6.2/V6.5/V6.9 human checklist to someone at a Mac.
- `2026-09-07` — **P4.5 done — GoTab.app packages, installs, and uninstalls cleanly (D38). Phase 4 is
  complete.** `build.sh` writes a full Info.plist (`plutil -lint` clean, `LSUIElement`, split
  `CFBundleShortVersionString`=`0.1.0` / `CFBundleVersion`=`git describe`, `CFBundleInfoDictionaryVersion`),
  signs the bundle ad-hoc and `codesign --verify --strict`s it. `install.sh` / `uninstall.sh` honour a
  `GOTAB_APPS` prefix, verify the installed copy's signature, and `--purge` also `tccutil reset`s both
  grants — round-tripped against a temp dir, everything came back clean. `cmd/gotab` with no flags
  runs the switcher when the executable is inside `.app` (a Finder launch) and prints usage from a
  shell. `spctl` rejects the ad-hoc bundle, which is expected — Gatekeeper only enforces that on a
  quarantined (downloaded) copy; a locally built one runs. **V6.7 is now purely the machine half:** a
  first launch on a clean account, and a real macOS 12 host.
  **Next: Phase 5 (localization scaffold, VoiceOver, update mechanism) — all optional polish — or
  stop here.** The switcher is feature-complete: `gotab -switch` enumerates, filters, orders, draws,
  prefetches, restyles, and raises, driven by a configurable ⌥⇥, with a settings window, onboarding,
  and an installer. What is left is Phase 5 polish and the V6 checklist a human runs on a Mac
  (V6.1–V6.5, V6.7, V6.9).
- `2026-09-07` — **Phase 5 carved for a 3-way fan-out; agents dispatched.** The carve commit froze
  the shared surface, exactly as `7e7751e` did for Phase 2 and the `panel.h` freeze for Phase 3:
  new `internal/i18n` (pure Go — `T(key)`, embedded `en.json`, `<locale>.lproj/gotab.json`
  overrides via an atomic swap, no per-call lock) and new `internal/update` with `update.go`'s
  `Check` / `Result` / `FeedURL` frozen and a stub `check` in `http.go`. `scripts/build.sh` now
  bundles `resources/*.lproj/` + `resources/appcast/` and declares `CFBundleLocalizations`
  (`en`, `de`). `cmd/gotab` gained `-check-update` (calls `update.Check`; short-circuits a `dev`
  build) and `initLocale()` (`GOTAB_LOCALE` → `LANG`), and its permissions-onboarding strings now
  resolve through `i18n.T`. **Two decisions locked:** P5.3 is a plain version check, not Sparkle
  (**D39**); the i18n `.strings`/`.json` split and locale-selection punt are **D40**. `check.sh`
  green on the carve. Contracts in `docs/tasks/P5.{1,2,3}.md`; `internal/i18n/*.go`,
  `internal/update/update.go`, `build.sh`, `cmd/gotab/`, `internal/app/` frozen for the duration.
  **Next: integrate P5.1/P5.2/P5.3 as they land — `[x]` them here, add V6.10 (VoiceOver heard by a
  human) and V6.11 (update check against a real host), and note `internal/i18n` / `internal/update`
  in `ARCHITECTURE.md`.**
- `2026-09-07` — **Phase 5 integrated and closed (D41). All three fan-out branches landed.** P5.1
  was complete in its worktree; P5.3 was uncommitted work finished and verified here; P5.2 was
  unstarted and built this session. Merged `merge: P5.1` / `merge: P5.3` / `merge: P5.2`, then this
  consolidation.
  **P5.1 (D40, carve):** `permissions.m`'s six alert strings go through `NSLocalizedString`;
  `resources/{en,de}.lproj/Localizable.strings` for AppKit and `resources/de.lproj/gotab.json` for
  Go, key sets matching `en.json` exactly, `plutil -lint` clean. Scratch check confirmed the loader:
  default → English, `SetLocale("de")` + `Load` → German, back to `en` → English.
  **P5.2 (D41):** `GTTileView` is now an accessibility container (group role, "Window switcher");
  each tile is a `GTTileElement : NSAccessibilityElement` (button role, `"<title>, <app>"` label,
  `accessibilityFrameInParentSpace` = the tile's own top-left frame, `isAccessibilitySelected`).
  `panel_populate` posts `NSAccessibilityAnnouncementRequestedNotification` (High) +
  `SelectedChildrenChanged` when the selection moves and `LayoutChanged` when the count changes;
  `g_a11y_last_*` debounce it, `gt_panel_hide` re-arms it. `settings.m` labels the two steppers, the
  Appearance popup, the blocked-apps field and the hotkey recorder. Press is a deliberate no-op
  (`panel.h` frozen, no raise callback) — activation still travels modifier-release.
  **P5.3 (D39, carve):** `internal/update` GETs `FeedURL` (5 s / 64 KiB bounded), decodes the
  manifest (`manifest.go`, rejects trailing data / blank version|url), compares the numeric triple
  (`version.go`, leading `v` and `-suffix`/`+build` dropped, missing fields = 0). Verified end to
  end against a local `http.server`: available / up-to-date / bad-URL / dev paths all correct.
  Stdlib only. `min_macos` decoded, not enforced.
  Two new Phase 6 rows: **V6.10** (VoiceOver actually heard — a human) and **V6.11** (the check run
  against a published `FeedURL` — needs a host). `ARCHITECTURE.md` now names `internal/i18n` and
  `internal/update` in the layer map. `check.sh` green on `main`; `build.sh` links the signed
  universal `.app`.
  **Next: Phase 6 — verification.** The switcher is feature-complete; what remains is the V6
  checklist run against the assembled app, most of it needing a human at a Mac (V6.1, V6.2, V6.9,
  V6.10) or a machine/host (V6.7, V6.11). V6.1/V6.2 are the cheap Phase 0/3 debts to run first.
- `2026-09-08` — **CI/CD: a `v*` tag now cuts a release, and the update feed has a real host (D42).**
  `.github/workflows/release.yml` runs the gate, builds the universal `.app` with the tag as
  `CFBundleShortVersionString` (`build.sh` gained a `GOTAB_SHORT_VERSION` override; `ci.yml` gained
  `fetch-depth: 0` so `git describe --tags` resolves), `ditto`-zips it, and `gh release create`s a
  GitHub Release with `GoTab-<tag>.zip` + its `.sha256`. It then renders `latest.json` from
  `resources/appcast/latest.json` — now the manifest **template**: `min_macos` and the shape live
  there, `version` / `url` / `notes` come from the tag — and publishes it to GitHub Pages via
  `actions/deploy-pages` (source = "GitHub Actions", no `gh-pages` branch). `FeedURL` moves off the
  never-hosted `gotab.app` to `https://moustafa-elgammal.github.io/gotab/latest.json`. First-party
  `actions/*` + `gh` only. Still check-only (D39); the zip is ad-hoc signed (D38), so a downloader
  needs right-click → Open until a notarization step exists. **New row V6.12** (the pipeline runs
  once end to end); **V6.11** now depends on it. **Manual, once the repo is public:** Settings →
  Pages → Source → "GitHub Actions", then `git tag -a v0.2.0 -m "…" && git push origin v0.2.0`.
  **Next: unchanged** — Phase 6 verification proper; V6.1/V6.2 remain the cheap human debts.
- `2026-09-08` — **Licensed GPL-3.0-or-later** ahead of publishing the repo. `LICENSE` holds the
  verbatim FSF text (`gnu.org/licenses/gpl-3.0.txt`); README gained a `## License` section with the
  short notice and the copyright line. Per-file `SPDX-License-Identifier` headers are a possible
  follow-up, not done here.
- `2026-09-08` — **V6.12 ran end to end: `v0.2.0` is cut and the feed is live.** The `release` job
  (build, sign, zip, `gh release create`, render manifest) passed first try; the downloaded asset
  verifies (SHA-256 matches, `CFBundleShortVersionString` `0.2.0`, `codesign --verify --strict` ok).
  `deploy-pages` needed two repo-settings fixes, now both in `docs/tasks/V6.12.md`'s prereqs:
  enabling Pages, and adding a **Ref type: Tag `v*`** rule to the `github-pages` environment (the
  default allows only the default branch, so a tag deploy is rejected). The feed serves at the
  `github.io` `FeedURL`, 301-redirecting to the account's custom domain `elgx.me/gotab/latest.json`;
  `FeedURL` stays on `github.io` on purpose. One `release.yml` bug fixed in the same commit as this
  line: `actions/checkout` shadows the annotated tag with a lightweight ref, so `notes` rendered as
  the commit subject — a `git fetch --force origin refs/tags/<tag>:refs/tags/<tag>` before reading
  it is the fix (v0.2.0's already-published body keeps the old text). **Next:** V6.11 —
  `gotab -check-update` against the now-live host — plus the Phase 6 human/machine debts.
- `2026-09-08` — **App icon** (D43). The delivered art `resources/assets/ico.png` (gopher, 496×664
  portrait, its own sticker outline) is centred on a transparent 1024² square at 92% of the tile
  and committed as `resources/assets/icon-1024.png`; `build.sh` renders that to
  `Contents/Resources/AppIcon.icns` with `sips` + `iconutil` and the plist gains `CFBundleIconFile`.
  The square master is committed so the build stays on base-system tools (no Swift); the one-time
  padding recipe is in `resources/assets/README.md`. 128 px and up look right; 16/32 px are muddy —
  the art is detailed, a simplified small-size glyph is future work. `LSUIElement` means no Dock
  tile, but Finder, login items and the TCC prompts all show it.
- `2026-09-08` — **Phase 6 started — the four tasks an agent host can run are done (V6.6, V6.8,
  V6.11, V6.12).**
  **V6.6:** the table tests Phases 2–5 deferred (D16/D23), for every *pure* surface they grew —
  `internal/prefs` (schema, `Load`/`Save` round trip, the `-prefs Key=Value` parser and its
  rejections), `internal/i18n` (`normalize`, the override→base→key fallback, a `t.TempDir` overlay),
  `internal/update` (`parseVersion`/`compare`, `decodeManifest`, and `check` end to end over
  `httptest` including every error path), the event loop's `handle` (summon→cycle→dismiss, cycle
  before summon is a no-op, only Quit stops it), and `HotkeyDisplay` — the one pure function in
  `internal/platform/darwin`; nothing else there is unit-tested (ARCHITECTURE.md; that is V6.5's).
  Contract `docs/tasks/V6.6.md`. Test-only, no production code touched.
  **V6.8:** every `internal/core` benchmark still `0 allocs/op`, and a new `internal/app`
  `BenchmarkHandleGesture` (Summon → Cycle → Cycle → Dismiss, 20 real windows) is
  `19.8 ns/op, 0 allocs/op` — filtering + rebuilding the order every rescan (P4.4) cost the hot path
  nothing. The Phase 3 `assumption` is discharged as **D44**: `panelRenderer.onState`'s two slice
  allocations are measured (2 allocs, 784 B at 7 windows / 2240 B at 20, once per gesture) and
  **accepted** — not a `core` hot path, bounded by window count, below the noise of the ~46 ms
  capture; the pooled-buffer fix is on record if a profile ever wants it. Contract `docs/tasks/V6.8.md`.
  **V6.11:** `gotab -check-update` run against the live `github.io` feed (then advertising `0.2.1`):
  a lower local version prints the "available" line + notes + the tag URL, `99.0.0` prints "up to
  date", a dead proxy prints the one-line warning and exits 0. All three green; the redirect to the
  custom Pages domain is followed transparently. `httptest` regression coverage landed under V6.6.
  Contract `docs/tasks/V6.11.md`. The P5.3 `assumption` ("`FeedURL` has no host") is discharged.
  **V6.12:** already run end to end for `v0.2.0` (D42) and again for `v0.2.1` (the app-icon release);
  the feed serves `0.2.1`. Marked done.
  **Comment drift noted, not fixed:** `internal/prefs/prefs.go` + its `doc.go` still say the filter
  is "schema-only" and "the event loop does not consult Rules yet" — P4.4/D37 wired both. A docs-only
  follow-up.
  **Next: the human/machine checklist — V6.1–V6.5, V6.7, V6.9, V6.10.** V6.1 (hotkey latency) and
  V6.2 (panel over full-screen / across Spaces) are the cheap Phase 0/3 debts to run first, at a Mac.
  Each still needs its `docs/tasks/V6.N.md` written when picked up.
- `2026-09-08` — **Phase 6 prep: the eight human/machine tasks now have turnkey contracts, and the
  comment drift is fixed.** No further V6 task can run on an agent host — V6.1/V6.9/V6.10 need a
  person at the keyboard, V6.2 needs ≥ 2 Spaces + a full-screen app, V6.3/V6.4/V6.5 need a window
  server, V6.7 needs a clean account + a macOS 12 host — so the forward motion available here was to
  write `docs/tasks/V6.{1,2,3,4,5,7,9,10}.md`: prerequisites, the exact commands / screenshots /
  numbers to capture, the acceptance bar, and where a negative result sends the work (P0.2, P3.1,
  P3.3, P2.5, P4.3, P4.5, P5.2). The Phase 6 table marks these `☐ ready` — contract written, needs a
  machine. Also cleared the drift D41's log flagged: `internal/prefs/prefs.go` + `doc.go` said the
  filter was "schema-only" and the loop "does not consult Rules yet" — P4.2 (D35) wired the hotkey
  chord and P4.4 (D37) wired the filter; only `Rules().ActiveAppOnly` is still inert (needs the
  frontmost pid on Summon) and the comments now say exactly that. Gate green; test-and-docs only.
  **Next: unchanged — the V6 checklist at a Mac, V6.1 and V6.2 first.**
- `2026-09-08` — **On-machine session: V6.1 passes, V6.5 opens Phase 7.**
  **V6.1 (D45):** 20 real ⌥⇥ presses — worst case event→callback **3.2 ms, 64 % of the 5 ms budget**
  (mean 0.6, p50 0.28); first C→Go crossing 28 µs and not the cold-thread outlier P0.2 feared; no
  `kCGEventTapDisabledByTimeout`. P0.2's box → `[x]`; Phase 0's last unmeasured target now has a
  number. Two blips at 2.5 / 3.2 ms, still inside.
  **V6.5 (D46):** enumeration on a live desktop is correct — the join, `cg`-recovery and
  exclusion-with-reason all check out. **Raise works for AX-visible windows and fails
  (`ErrNoWindow`) for every `cg`-only one** — `gt_window_raise` resolves through
  `kAXWindowsAttribute`, which does not list other-Space windows, so the switcher renders tiles it
  cannot action. In one `-switch` session 12/12 selections onto `cg`-only tiles failed.
  **`spike/raisetest/` made it concrete** (3/3 AX-visible raised, the `cg`-only one did not).
  **New Phase 7 — Actionability:** P7.1 fall back to activating the owning app when the window will
  not resolve but its pid is alive; P7.2 prune windows once the pid is gone; P7.3 (optional)
  real Space-switch-then-raise; P7.4 document the default (keep app-reachable windows). Verification
  is V6.13 / V6.14 in the Phase 6 table (a new phase gets rows there, not its own tests).
  **Also noted, unfiled:** a Finder / `open -a` launch of `GoTab.app` did not fire the ⌥⇥ tap while a
  direct exec of the same binary did — smells like a TCC grant bound to an older build's signature
  (`AXIsProcessTrusted` true, tap silent — the P0.2 failure mode). To chase under V6.9.
  **Next: land Phase 7 (P7.1 first), then re-run V6.5 / V6.13; the rest of the V6 checklist stands.**
- `2026-09-08` — **P7.1 landed (code only; V6.13 verifies on a machine).** `gt_window_raise` now
  falls back to `activate_app(pid)` and returns `GT_OK` when a window id will not resolve to an
  `AXUIElement` with status `GT_ERR_NO_WINDOW` **and** the owning pid is a running application —
  the `cg`-only / other-Space tiles D46 found the switcher could not action. `copy_window_element`
  hands the owning pid back even on an empty return; `app_is_alive`
  (`runningApplicationWithProcessIdentifier:`) draws the line between the fallback and `ErrNoWindow`,
  which is still returned when the owner is gone (→ P7.2). `GT_ERR_TIMEOUT` / `GT_ERR_NOT_TRUSTED`
  are not rerouted — a wedged app or a revoked grant is a different answer. Changes confined to
  `action.{h,m,go}`; `Raise`'s signature and `Minimize`/`Unminimize`/`Close` are untouched. Gate
  green, no tests added (D16 / D23). Contract + design notes in `docs/tasks/P7.1.md`; the
  `assumption` (fallback reaches the intended app; nil is the right contract for `Loop.Activate`) is
  tagged at P7.1 pointing to V6.13.
  **Next: P7.2 (dead-window prune in `doRescan`), then P7.4 (document the keep-by-default rule);
  P7.3 optional. On a Mac: V6.13 / V6.14, then the rest of the V6 checklist.**

