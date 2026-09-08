# Architecture

> Read this before touching code. It is deliberately short so every agent can afford to read all of it.
> If you need more than this file plus your task file, the task is scoped wrong — say so instead of guessing.

## What this is

A macOS window switcher written in Go, built from scratch: **its own bundle ID, no license
migration, no Sparkle continuity, no preference migration.** Nothing on a user's machine is inherited.

Goal, in priority order:
1. **Feature parity with the core switching a keyboard window switcher is expected to do.** The
   switcher itself, not a whole product surface. This is the reason the project exists.
2. **Learn Go properly** — idiomatic concurrency, explicit resource lifecycle, zero-alloc hot paths.
3. **Stay within a sane memory budget** — bounded caches, no leaks. A correctness requirement, not a
   competition with any other switcher; see D10 for why that framing was dropped.

Non-goals: cross-platform, macOS < 12 (the toolchain forces `minos 12.0` — measured on Go 1.26, not
assumed; D2 said 11.0 and D17 corrects it), Pro/licensing.

## The four layers

```
  cmd/gotab                    main(), LockOSThread, hands the thread to AppKit
        │
  internal/app                 event loop: the ONE goroutine that owns mutable state
        │
  internal/core   ◄── pure Go, zero cgo, zero macOS. All decisions live here.
        │                      ordering · filtering · search · selection · tab groups · layout
        │
  internal/prefs  ◄── pure Go. The settings schema; the store is behind a Reader/Writer.
  internal/i18n   ◄── pure Go. String catalog: embedded en.json + a <locale>.lproj/gotab.json overlay.
  internal/update ◄── pure Go, stdlib only. Fetches a release manifest and compares versions.
        │
  internal/platform/darwin     cgo + Objective-C. All IPC, all AppKit, all bitmaps, the prefs store.
```

`internal/core` is complete (Phase 1) plus the P3.2 layout engine. `internal/platform/darwin`
enumerates the switchable set (with each window's Space), observes window events, raises / minimizes /
closes windows (Phase 2, D24–D27), draws the panel sized for its display, prefetches thumbnails,
tracks the appearance, taps a configurable hotkey (Phase 3, D28–D33), reads/writes the CFPreferences
domain, shows a native settings window, onboards missing permissions, and ships as a signed universal
`.app` (Phase 4 complete, D34–D38). The panel and the settings window expose themselves to a screen
reader and the permissions alert resolves through `NSLocalizedString` (Phase 5, D39–D41).
`internal/prefs` is the pure-Go settings schema. `internal/i18n` is the pure-Go string catalog —
`T(key)` over an embedded `en.json` with an optional `<locale>.lproj/gotab.json` overlay, swapped
through an atomic pointer, no cgo (locale detection is deferred, D40); AppKit reads the sibling
`Localizable.strings` for the same locale. `internal/update` is pure Go, standard library only —
`Check` GETs a JSON manifest and compares versions, no Sparkle (D39); it downloads nothing.
`internal/app` owns the event loop — one goroutine, no mutex (P2.7 / D22) — and it filters the window
set through `core.Rules` (P4.4). `cmd/gotab` with no flags runs the switcher from inside `.app`;
`-settings`, `-permissions`, `-prefs`, `-check`, `-check-update`, `-watch` are the rest. Nothing has
run any of it on a real screen — an agent host has no window server, and none has been heard by a
screen reader — so the pixels, the granted hotkey round trip, the settings window, the permissions
modal, a clean-account launch, VoiceOver, and the update check against a live host are Phase 6's
(V6.1–V6.5, V6.7, V6.9–V6.11).

**The dependency arrow never reverses.** `core` must never import `platform`. `core` compiles and tests on
any OS, which is what makes it fast to develop and cheap to fan out across agents.

## The cgo rule

Measured on this machine (`spike/cgobench`), against a C function that does nothing:

| call | cost |
|---|---|
| pure Go | 2.58 ns |
| Go → C | **31.23 ns** (12x) |
| C → Go callback | **38.77 ns** (15x) |

Real AX calls add Mach IPC on top of that. Therefore:

- **Batch every crossing.** Never `for w := range windows { C.getTitle(w) }`. One call returns a packed
  array of all N windows. Amortise the 31 ns over the batch, never pay it per item.
- **Callbacks do nothing but enqueue.** An AX/SkyLight callback pushes onto a channel and returns. No
  allocation, no logic, no logging in the callback body. Work happens on the event-loop goroutine.
- **No cgo in `internal/core`.** Enforced by review and by the fact that `core` has no build tags.

## The memory rule

Not a competitive target any more (D10), but still the thing most likely to go wrong: a switcher that
leaks bitmaps degrades the longer it runs, and Go gives you no help here.

Window thumbnails are `CGImage`/`IOSurface` — **allocated by CoreGraphics, outside the Go heap**. The Go GC
sees an 8-byte pointer where a multi-megabyte bitmap is pinned. It therefore feels no memory pressure and
will not collect it. Swift's ARC releases these deterministically today; **Go will not do this for you.**

The rules:

1. **C owns every bitmap.** Go holds an opaque handle (`platform.ImageRef`), never a `*C.struct_CGImage`
   it is tempted to treat as memory the runtime understands.
2. **Explicit release, always.** Every acquire has a matching `Release()`. `runtime.SetFinalizer` is a
   backstop for leak detection in debug builds, **never** the primary mechanism — finalizers run at GC's
   convenience, which is exactly the problem we're solving.
3. **The cache is bounded and that bound is a number, not a hope.** A fixed-capacity LRU keyed by window id.
   Bound it for **peak footprint and fault-in latency**, not resident size: macOS already reclaims idle
   CG raster pages on its own (D9, confirmed against a comparable switcher in D10), so the bound buys
   predictability and a warm cache on summon, not a smaller steady state.
4. **Thumbnails are downscaled at capture time**, never captured full-res then shrunk.
5. **When memory is measured at all, use D8's instrument** — `vmmap --summary` -> `CG raster data`, plus
   `Physical footprint (peak)`. Never instantaneous `phys_footprint`: it silently under-reports bitmaps
   (that mistake produced D4). Measurement is order-sensitive; CG raster pages go resident only while
   recently touched, so sample during a summon. `spike/procmem` does this for any pid.

## The actionability rule

The enumeration join (`internal/platform/darwin/window.go`, P2.3c / D21) deliberately shows windows
Accessibility cannot see — usually on another Space. Every such tile must still *do something* when
it is picked, or it should not be a tile (D46 / D47):

- **A window that will not resolve to an `AXUIElement` is still actionable** while its owning process
  is alive. `Raise` first tries to switch to the window's Space and resolve it for real (P7.3 —
  SkyLight, private, optional, may be unavailable); failing that it activates the owning application
  (P7.1). The user reaches what they aimed at even when the exact window cannot be ordered. Only a
  dead owner is `ErrNoWindow`; `ErrTimeout` / `ErrNotTrusted` are still their own answers.
- **A window leaves the model only when it is genuinely gone** (P7.2): absent from both enumerations
  for a full rescan, or its owning pid no longer exists. A stale CoreGraphics entry does not keep it
  on screen.
- **The default is to keep an app-reachable window visible**, not hide it: it is useful (it reaches
  the app), and hiding it makes the list shorter and less predictable. There is no `prefs` field to
  hide these windows — add one only if that turns out to be wanted (P7.4).

## Threading

macOS demands AppKit on the main thread; Go wants to schedule goroutines freely. The reconciliation:

- `main()` calls `runtime.LockOSThread()` **before anything else**, then hands that thread to AppKit's run
  loop. That thread is AppKit's forever.
- All Go logic runs on other goroutines. State lives in **one** event-loop goroutine — share by
  communicating, not by locking. No mutex around the window model.
- UI mutations marshal back via `dispatch_async(dispatch_get_main_queue())`. Never touch AppKit from a
  non-main goroutine; it asserts at runtime.
- Latency-critical ordering, a hard-won lesson of the platform (PLATFORM-LESSONS.md §6): on summon,
  **draw first, do bookkeeping after**. Nothing that can block on IPC runs before the pixels the user
  is waiting for.

## Testing

- `internal/core` — table-driven `go test`, runs anywhere, must stay fast. This is where coverage lives.
- `internal/platform` — **not unit-tested.** It is the humble object: IPC and AppKit only, verified at
  runtime by the spikes and by hand. Do not chase coverage here.
- Every core package ships `doc.go` stating its invariants in prose. Agents read that instead of the source.
- **New work writes no tests as it goes, in any phase** (D16, generalised in D23). Verification is
  batched into the final phase. That changes *when* things are checked, not what is checkable: the two
  bullets above still decide where coverage can meaningfully live, and `scripts/check.sh` must stay
  green throughout.

## Status

Phase 0 is closed (D15). Three of its four unknowns are settled on measurements — the panel (D13),
ScreenCaptureKit capture (D12) and bitmap release (D14) — and the fourth, hotkey delivery latency, is
code without a number and is carried as an explicit assumption into V6.1. What Phase 0 bought is knowing
where the 100 ms summon budget goes: on data, not on pixels, and not on capture, which cannot happen on
the summon path at all.

Phases 2, 3 and 5 each began "serial" and each fanned out once its shared surface was frozen — the C
shim for Phase 2 (`7e7751e`), `panel.h` for Phase 3, `internal/i18n` + `internal/update/update.go`
for Phase 5. All closed: Phase 2 in D24–D27, Phase 3 in D28–D33, Phase 4 in D34–D38, Phase 5 in
D39–D41. `gotab -switch` is the switcher — ⌥⇥ tap → enumerate → order → lay out → draw → prefetch →
restyle → raise, on a live run loop — with a settings window, permissions onboarding, an installer,
a localized permissions alert, a screen-reader-visible panel, and `gotab -check-update`.

**All that is left is Phase 6 — verification against the assembled app.** An agent host has no window
server and no screen reader, so the pixels, the granted hotkey round trip, VoiceOver, a clean-account
launch, and the update check against a live host are unseen (V6.1–V6.11); a bad result there sends
work back into the phase that produced it. See [ROADMAP.md](ROADMAP.md).
