# Decisions & findings

Append-only. Newest last. Agents **append**; they never rewrite an entry. Each entry records what was
measured, not what was assumed — if there is no measurement, say so explicitly.

---

## D1 · cgo boundary costs 12–15x a native call — 2026-09-06

Measured (`spike/cgobench`, this machine, against a C function doing `x+1`):

| call | ns/op |
|---|---|
| pure Go | 2.58 |
| Go → C | 31.23 (12x) |
| C → Go callback | 38.77 (15x) |

**Consequence:** every cgo crossing is batched. One call returns N windows, never one call per window.
Callbacks enqueue and return; no logic in a callback body. This is the origin of the batching rule in
`ARCHITECTURE.md`.

## D2 · Go cannot build below macOS 11 — 2026-09-06

Built with `CGO_CFLAGS=-mmacosx-version-min=10.14`. The Go linker overrode it: `otool -l` reports
`minos 11.0`. AltTab ships `MACOSX_DEPLOYMENT_TARGET = 10.14.4`.

**Consequence:** macOS 10.14/10.15 are out of scope, permanently. Recorded as accepted at project start
(the "clean break" decision), not as a regression to fix.

## D3 · Window thumbnails require ScreenCaptureKit — 2026-09-06

`CGWindowListCreateImage` is **obsoleted in macOS 15** — the SDK marks it `unavailable`, so it is a
compile error, not a deprecation warning. Discovered when the spike failed to build.

**Consequence:** thumbnail capture must use ScreenCaptureKit, which is asynchronous and block-based.
From Go that means ObjC block trampolines and C→Go callbacks (D1), which is materially harder than a
synchronous call. **P2.6 is the highest-risk task in Phase 2** and should be prototyped before Phase 2
is planned in detail. AltTab links it weakly (`-weak_framework ScreenCaptureKit`) precisely because of
version skew; expect to do the same.

## D4 · We cannot yet measure CoreGraphics memory — P0.4 UNRESOLVED — 2026-09-06

The point of the project is lower memory, so this one matters most.

Allocated 200 CGImages of 800x600 RGBA — `CGImageGetBytesPerRow × height` totals **366 MB**. Measured
via `task_info(TASK_VM_INFO)`:

| stage | phys_footprint | resident_size |
|---|---|---|
| baseline | 4.0 MB | — |
| holding 200 images | 12.3 MB | 19.4 MB |
| after explicit release | 6.0 MB | 13.3 MB |

**366 MB of bitmaps produced ~15 MB of measured growth.** Either CoreGraphics defers materialising the
backing store until the image is drawn, or it allocates it as purgeable memory that neither
`phys_footprint` nor `resident_size` counts. Writing incompressible noise into the backing store did not
change the result, which rules out page sharing/compression as the explanation.

**Conclusion: the spike does NOT validate the memory rule.** It shows explicit release runs without
crashing; it shows nothing about reclaim, because the memory was never observed to be held in the first
place. The original "GATE PASS" output was wrong and has been removed from the spike.

**Consequence — and this is the important part:** *measuring* memory is itself a prerequisite task, ahead
of any optimisation work. You cannot optimise what you cannot measure, and the obvious instruments are
blind here. P0.4 is split:

- **P0.4a** build a trustworthy instrument (`vmmap` region accounting, the `footprint` CLI, or Instruments'
  allocations template) and prove it reports ~366 MB for this exact spike
- **P0.4b** only then re-run the hold/release gate against real ScreenCaptureKit output

Until P0.4a reports a number that matches the arithmetic, **no memory claim about this project is
credible** — including any claim that it beats AltTab.

## D5 · Window enumeration: 57 ms cold, 0.30 ms warm — 2026-09-06

`CGWindowListCopyWindowInfo` for ~18 windows in one cgo call. The cold cost is framework load plus a first
WindowServer round trip; the summon path only ever pays the warm cost.

**Consequence:** 0.30 ms is comfortably inside the 100 ms summon budget, so batched enumeration is viable.
But the 57 ms cold cost must be paid at launch, not on first summon — warm the WindowServer connection
during startup or the very first ⌥⇥ of a session blows the budget.
