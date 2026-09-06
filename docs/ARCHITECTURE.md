# Architecture

> Read this before touching code. It is deliberately short so every agent can afford to read all of it.
> If you need more than this file plus your task file, the task is scoped wrong — say so instead of guessing.

## What this is

A macOS window switcher written in Go. Clean-slate rewrite of AltTab: **new bundle ID, no license
migration, no Sparkle continuity, no preference migration.** Nothing on a user's machine is inherited.

Goal, in priority order:
1. **Lower memory than AltTab** — see [The memory rule](#the-memory-rule-most-important). This is the reason the project exists.
2. **Learn Go properly** — idiomatic concurrency, explicit resource lifecycle, zero-alloc hot paths.
3. Feature parity with AltTab's core switching. Not its whole surface.

Non-goals: cross-platform, macOS < 11 (Go's linker forces `minos 11.0` — measured, not assumed), Pro/licensing.

## The four layers

```
  cmd/gotab                    main(), LockOSThread, hands the thread to AppKit
        │
  internal/app                 event loop: the ONE goroutine that owns mutable state
        │
  internal/core   ◄── pure Go, zero cgo, zero macOS. All decisions live here.
        │                      ordering · filtering · search · selection · tab groups
        │
  internal/platform/darwin     cgo + Objective-C. All IPC, all AppKit, all bitmaps.
```

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

## The memory rule (most important)

This is why the project exists, and it is the thing most likely to go wrong.

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
   AltTab retains one thumbnail per window indefinitely; any win comes from *this policy*, not from Go.
   **But see D9:** macOS already reclaims idle CG raster pages on its own, so the steady-state win over
   "retain everything" is unquantified until P0.7 measures AltTab. Bound the cache for peak footprint and
   fault-in latency, not on an assumption about resident size.
4. **Thumbnails are downscaled at capture time**, never captured full-res then shrunk.
5. **Every phase gate measures memory with D8's instrument** — `vmmap --summary` -> `CG raster data`, plus
   `Physical footprint (peak)`. Never instantaneous `phys_footprint`: it silently under-reports bitmaps
   (that mistake produced D4). Measurement is order-sensitive; reading pixels faults pages back in.

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

## Status

Phase 0 (proof) is the gate. See [ROADMAP.md](ROADMAP.md). Nothing in Phases 1+ is committed work until the
Phase 0 gate passes — if the spike can't hit the latency and memory targets, the design changes or the
project stops, and that is a legitimate outcome.
