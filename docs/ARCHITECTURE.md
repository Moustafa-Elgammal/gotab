# Architecture

> Read this before touching code. It is deliberately short so every agent can afford to read all of it.
> If you need more than this file plus your task file, the task is scoped wrong — say so instead of guessing.

## What this is

A macOS window switcher written in Go. Clean-slate rewrite of AltTab: **new bundle ID, no license
migration, no Sparkle continuity, no preference migration.** Nothing on a user's machine is inherited.

Goal, in priority order:
1. **Feature parity with AltTab's core switching.** The switcher itself, not its whole surface. This is
   the reason the project exists.
2. **Learn Go properly** — idiomatic concurrency, explicit resource lifecycle, zero-alloc hot paths.
3. **Stay within a sane memory budget** — bounded caches, no leaks. A correctness requirement, not a
   competition with AltTab; see D10 for why that framing was dropped.

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
        │
  internal/platform/darwin     cgo + Objective-C. All IPC, all AppKit, all bitmaps, the prefs store.
```

`internal/core` is complete (Phase 1) plus the P3.2 layout engine. `internal/platform/darwin`
enumerates the switchable set (with each window's Space), observes window events, raises / minimizes /
closes windows (Phase 2, D24–D27), draws the panel sized for its display, prefetches thumbnails,
tracks the appearance, taps a configurable hotkey (Phase 3, D28–D33), reads/writes the CFPreferences
domain, shows a native settings window, and onboards missing permissions (Phase 4 so far, D34–D37).
`internal/prefs` is the pure-Go settings schema. `internal/app` owns the event loop — one goroutine,
no mutex (P2.7 / D22) — and it filters the window set through `core.Rules` (P4.4). `cmd/gotab -switch`
is the switcher end to end on a live AppKit run loop; `-settings` opens the settings window,
`-permissions` walks the grants, `-prefs` / `-check` are the CLIs. Nothing has run any of it on a real
screen — an agent host has no window server — so the pixels, the granted hotkey round trip, the
settings window, and the permissions modal are Phase 6's (V6.1–V6.5, V6.9).

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
   CG raster pages on its own (D9, confirmed against AltTab in D10), so the bound buys predictability and
   a warm cache on summon, not a smaller steady state.
4. **Thumbnails are downscaled at capture time**, never captured full-res then shrunk.
5. **When memory is measured at all, use D8's instrument** — `vmmap --summary` -> `CG raster data`, plus
   `Physical footprint (peak)`. Never instantaneous `phys_footprint`: it silently under-reports bitmaps
   (that mistake produced D4). Measurement is order-sensitive; CG raster pages go resident only while
   recently touched, so sample during a summon. `spike/procmem` does this for any pid.

## Threading

macOS demands AppKit on the main thread; Go wants to schedule goroutines freely. The reconciliation:

- `main()` calls `runtime.LockOSThread()` **before anything else**, then hands that thread to AppKit's run
  loop. That thread is AppKit's forever.
- All Go logic runs on other goroutines. State lives in **one** event-loop goroutine — share by
  communicating, not by locking. No mutex around the window model.
- UI mutations marshal back via `dispatch_async(dispatch_get_main_queue())`. Never touch AppKit from a
  non-main goroutine; it asserts at runtime.
- Latency-critical ordering, inherited from AltTab's hard-won experience: on summon, **draw first, do
  bookkeeping after**. Nothing that can block on IPC runs before the pixels the user is waiting for.

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

Phases 2 and 3 both began "serial" and both fanned out once their shared surface was frozen — the C
shim for Phase 2 (`7e7751e`), `panel.h` for Phase 3. Both are closed: Phase 2 in D24–D27, Phase 3 in
D28–D33. `gotab -switch` is the switcher — ⌥⇥ tap → enumerate → order → lay out → draw → prefetch →
restyle → raise, on a live run loop. **The only Phase 3 debt is on-screen verification** (V6.1–V6.5):
an agent host has no window server, so the pixels and the granted hotkey round trip are unseen, and a
bad result there sends work back into Phase 3. Next is Phase 4 (Product). See [ROADMAP.md](ROADMAP.md).
