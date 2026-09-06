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

## D6 · Project framing — why this project exists in this shape — 2026-09-06

Recorded because the reasoning came out of a conversation and would otherwise be lost. These are settled
decisions, not open questions.

**Goals, in priority order:** (1) lower memory than AltTab, (2) learn Go properly. Feature parity is a
means, not the goal.

**Scope decisions and what they bought:**

| decision | consequence |
|---|---|
| macOS only | no cross-platform constraints; full AppKit/private-API surface stays in play |
| clean break — new bundle ID | no Keychain/license migration, no 25 preference migrations to port, no Sparkle continuity. Removed the majority of the original risk. |
| full app replacement in Go | accepted with the cgo costs in D1 measured and understood up front |

**Rejected, with reasons, so they are not relitigated:**

- *Go core + Swift UI shell* — the pure kernels are the 14% of AltTab that does no IPC, so they are
  precisely the code with the least to gain from a port. The 86% that would benefit is the part Go cannot
  express.
- *Preserving macOS 10.14 support* — impossible, see D2.
- *Preserving Pro licensing* — dropped with the clean break. Note for any future reversal: AltTab
  `Keychain.swift:42` documents that a same-bundle-id build with a **different code signature** cannot read
  the item but *can* silently overwrite it. Same Developer ID cert + TeamID + bundle ID is the whole
  requirement, and it is all-or-nothing.

**An honest caveat on goal (1):** AltTab actual memory footprint was never measured — it was not running
during the analysis session. So "lower memory than AltTab" currently has **no baseline**. See P0.7. Until
both P0.4a (an instrument that works) and P0.7 (a number to beat) exist, the primary goal is unfalsifiable.

## D7 · Prior art is captured, not inherited — 2026-09-06

`docs/ALTTAB-LESSONS.md` distils AltTab hard-won platform knowledge: the two-plane architecture, the rule
that only an attention decision or structural repair may move MRU, the 60 ms settle, the macOS traps
(synchronous XPC inside AppKit, TCC responsible-process rule, the capture drain, the signal-mask hazard),
and the observability ceilings that cannot be engineered around.

Read it before designing any subsystem. It is the cheapest way to avoid re-deriving several years of
reverse-engineering, and it names its sources so each claim can be verified against the AltTab tree.

## D8 · P0.4a RESOLVED — the instrument is `vmmap`, and D4 was wrong — 2026-09-06

D4 concluded the instruments were blind to CoreGraphics memory. That was wrong, and the correction
matters more than the original finding.

**What was tested.** 200 CGImages of 800x600 RGBA (366.2 MB declared), against three hypotheses:

| hypothesis | verdict | evidence |
|---|---|---|
| H1 the bytes are never allocated | **rejected** | 0 of 200 contexts returned a NULL data pointer; 366.2 MB written byte-by-byte; `CGDataProviderCopyData` returns 366.2 MB whose content checksum is real noise, not zeros |
| H2 the instruments cannot see them | **rejected** | `task_info` phys, `vmmap` resident and `vmmap` dirty all agree within 0.1 MB |
| H3 identical pages are compressed away | **rejected** | identical and unique bitmap content behave the same; `compressed` stays 0.0 MB |

**The actual explanation.** `vmmap --summary` has a dedicated region type for exactly this:

```
Physical footprint:         11.9M
Physical footprint (peak):  374.4M
CG raster data     VIRTUAL 368.8M   RESIDENT (varies)   DIRTY (varies)
```

The bitmaps were fully resident — peak footprint 374.4 MB against 366.2 MB declared. **macOS reclaims
idle CG raster pages aggressively and faults them back in on access**, so an instantaneous
`phys_footprint` reading taken while the images sit untouched reports almost nothing. Reading the pixels
immediately before measuring showed `CG raster data` resident at the full 368.8 MB; measuring without
touching them showed 6.4 MB. Same images, same process — only the access pattern differed.

**The instrument, for anyone measuring memory in this project:**

1. `vmmap --summary <pid>` → the **`CG raster data`** row. Its VIRTUAL size is stable and accurate.
2. **`Physical footprint (peak)`** — the true high-water mark.
3. **Not** instantaneous `phys_footprint` or `resident_size`. Both are legitimate numbers that answer a
   different question, and using them here produces the D4 mistake.

Implemented in `spike/memprobe`. Note the measurement is order-sensitive: anything that reads the pixels
faults the pages back in, so measure before touching, and say which you did.

## D9 · The memory premise is weaker than assumed — 2026-09-06

Direct consequence of D8, and it deserves its own entry because it bears on why this project exists.

**macOS already evicts idle thumbnail pages.** AltTab retains one `CALayerContents` per window
indefinitely (`Window.swift:40`), and GoTab planned to beat that with a bounded LRU. But the OS is
already doing a form of that eviction for free: an untouched 366 MB of CG raster data sat at ~6 MB
resident without any policy from us.

So a bounded LRU would reduce *virtual* size and *peak* footprint, but the steady-state resident win over
"retain everything and let macOS reclaim" may be small. It is still worth doing — peak footprint is real,
eviction under pressure has a latency cost when pages fault back in during a summon, and unbounded growth
is a genuine risk with many windows — but **the size of the win is now an open question, not a given.**

**This raises the stakes on P0.7.** Until AltTab's real footprint is measured with D8's instrument, we do
not know whether the headline goal has meaningful room in it. Do P0.7 before designing the cache.

## D10 · Memory parity with AltTab is no longer a goal — 2026-09-06

**Decision (owner's call).** The project's goal is feature parity with AltTab's core switching, written
in Go. Using less memory than AltTab is dropped as an objective. D6's framing — "the reason this project
exists is lower memory" — is superseded; it is left in place because this file is append-only.

**Consequences**, all applied in the same commit:

- P0.7 dropped. Phase 0 is now de-risking (panel, hotkey, ScreenCaptureKit), not a go/no-go.
- The gate criterion "steady-state RSS, 50 windows < AltTab's" is removed. It was also unmeasurable as
  written — see the numbers below.
- `ARCHITECTURE.md`'s memory rule stays, reframed as **correctness**: a leaked bitmap is a bug whatever
  the goal is. The cache bound is justified by peak footprint and summon-time fault-in latency (D9).

**What P0.7 measured before it was dropped.** AltTab 11.6.0, 19 windows, macOS 26.6.2, measured with
`spike/procmem` (D8's instrument applied to another pid). Two runs:

| | run 1 (pid 30462) | run 2 (pid 31918, clean process) |
|---|---|---|
| `CG raster data` virtual | 110.0 -> 114.3 MB | 23.7 MB, flat |
| `CG raster data` resident, during summons | max 18.6 MB | max 21.6 MB (91% of virtual) |
| `CG raster data` resident, idle | **0.0 MB across 35 samples** | — |
| TOTAL dirty, active -> idle | 73.3 -> 9.9 MB | max 12.5 MB |
| `Physical footprint (peak)` | 281.3 -> 305.3 MB | 36.6 -> **70.5 MB** |

Run 1's peak is **not usable**: it includes AltTab's first-run onboarding and the permission-grant flow,
which happened before sampling started. Run 2 restarted AltTab so its peak starts clean at 36.6 MB, and
~10 summons took it to 70.5 MB. **Treat 70.5 MB peak / ~24 MB of retained thumbnails as the only
defensible figures**, and note the two runs disagree on retained thumbnail volume (114.3 vs 23.7 MB
virtual) by a factor of five, unexplained — run 1 had a longer and messier process history. The
measurement was stopped when the goal changed, so that discrepancy was never chased.

**The finding that outlives the goal:** idle `CG raster` resident went to **0.0 MB and stayed there** for
the whole idle tail. macOS evicts untouched thumbnail pages completely, on a timescale of seconds. Any
future memory claim about this project — or any other — must therefore quote peak footprint and virtual
size, and must sample *during* a summon. Steady-state resident is ~0 for any window switcher on this OS,
which makes it a useless basis for comparison. This is D9 confirmed against a real third-party target.

## D11 · `api.go` amended after Phase 1: `Cache.Capacity` unexported — 2026-09-06

`api.go` is frozen so parallel agents can code against it without coordinating. Freezing is only
meaningful if amendments are recorded rather than made silently, so: one type changed after the seven
Phase 1 tasks landed.

`Cache.Capacity` was an exported `int`. `NewCache` panics on `capacity <= 0`, but nothing stopped a
caller lowering it afterwards, which strands every entry above the new bound with **nobody ever told to
release them** — precisely the leak the bounded cache exists to prevent, and invisible to the Go GC
because the bitmaps live outside the heap. The P1.7 agent found this, defended what it could from
inside its own file (the overflow drop is a loop, so a lowered bound is at least fully reported on the
next insert), and escalated rather than editing the frozen file. That was the right call.

Now `capacity` is unexported and fixed at construction, read via `Capacity()`. `Touch` panics on a
zero-value `Cache`, which would otherwise degenerate to holding a single entry rather than failing.
Changed now because nothing outside `internal/core` consumes it yet; after Phase 2 codes against it the
same fix would be a migration.

**Two other things Phase 1 established, both consequences of the frozen shapes rather than choices:**

- `Order` holds a single `Rows []int` with no scratch field, which rules out every stdlib stable sort at
  0 allocs — `sort.SliceStable` boxes a `sort.Interface`, and a sorter closing over both `o.Rows` and
  `m.Focuses` escapes. `Rebuild` therefore uses an in-place insertion sort: stable, allocation-free, and
  fine at tens of windows. If window counts ever reach the hundreds this is the line to revisit.
- `Model.Remove` being swap-with-last means an `Order` built before a removal holds row indices past the
  end of the shortened `Model` until the next `Rebuild`. Bounds checks in `Selection` are load-bearing,
  not defensive noise, and `TestIntegration*` pins the sequence.

## D12 · P0.6: ScreenCaptureKit works from Go, but thumbnails cannot be captured on summon — 2026-09-06

`spike/sck`, macOS 26.6.2, M-series, 10 capturable windows on screen. Every number below is the mean of
repeated runs that agreed to within ~5%.

**The four questions P0.6 was written to answer:**

1. **Does the completion handler fire on a Go-owned thread with no NSRunLoop? Yes.** GCD delivers to its
   own queues and needs no run loop on the calling thread. The `dispatch_semaphore` timeout in
   `capture.m` never fired in normal operation. **Phase 2 does not have to marshal captures to the
   AppKit thread** — the biggest threading risk in the port is not real.
2. **Cost: ~46 ms warm, ~112 ms cold**, plus **~46 ms** for `getShareableContent`, measured separately.
3. **Release is clean.** 60 held images, 330.5 MB, drop to 64 K the moment they are released. No leak.
4. **Downscale at capture time works** — `SCStreamConfiguration.width/height` yields exactly the
   requested pixels.

**But two things nobody asked, and they are the ones that change the design.**

**(a) Capture is a fixed ~35-46 ms of overhead that does not parallelise.** The cost is independent of
output size — a 400x237 thumbnail and a full-res 1512x897 one both take ~45 ms, so it is round-trip
latency to the WindowServer, not pixel work. Issuing captures concurrently barely helps:

| concurrent captures | wall time | per capture |
|---|---|---|
| 1 | 46 ms | 46 ms |
| 2 | 79 ms | 39 ms |
| 4 | 138 ms | 35 ms |
| 8 | 260 ms | 33 ms |
| 10 | 324 ms | 32 ms |

Ten at once costs 324 ms against 460 ms serial — a 1.4x speedup, not 10x. `SCScreenshotManager`
serialises in the WindowServer; the asynchrony is real but there is no concurrency behind it.

**The consequence is a hard constraint on Phase 2 and Phase 3.** The summon budget is 100 ms for the
whole path. Enumeration alone is 46 ms and one thumbnail is another 46 ms, so **a summon can afford at
most one freshly captured thumbnail, and realistically zero.** Capturing on summon is off the table.
Thumbnails must already exist when the panel opens, which means:

- P2.6 captures in the **background, ahead of summon**, driven by the AX/CGS notifications of P2.3-P2.4.
- P3.1 must render the panel from whatever the cache holds and fill thumbnails in **asynchronously** —
  the panel cannot block on pixels. A tile with an app icon and no thumbnail is a state the renderer has
  to support from the start, not a later refinement.
- P1.7's bounded LRU is now load-bearing for **latency**, not just memory: a cache miss on summon is a
  visibly empty tile, so the eviction policy decides what the user sees, not just what is retained.

**(b) SCK thumbnails are IOSurface-backed and are invisible to D8's instrument.** Holding 60 full-res
images, `vmmap --summary` reports:

```
IOSurface                        330.5M       0K       0K   ...   61
Physical footprint:               7105K
Physical footprint (peak):        7185K
```

330.5 MB of surfaces across 61 regions, **0 K resident, 0 K dirty**, while the process footprint sits at
**7.1 MB**. There is no `CG raster data` region at all. The backing store lives in the WindowServer/GPU
and is only mapped into our address space.

This **corrects D8 and D9 for anything captured through SCK**. D8 concluded that `CG raster data` plus
`Physical footprint (peak)` account for bitmaps exactly, and that was true of images this process
allocated itself (`spike/memprobe` synthesises its own). It is false for SCK output: peak footprint —
D8's "only honest number" — read 7.1 MB while we held 330 MB. **The row to watch for thumbnails is
`IOSurface` virtual, and `spike/procmem` does not report it yet.** D9's "macOS evicts idle thumbnails to
~0" also needs restating: these pages were never resident in *our* process to begin with.

The good news in this is real: a bounded thumbnail cache costs almost nothing in our own footprint. The
memory rule in `ARCHITECTURE.md` still holds as correctness — 61 unreleased surfaces are 330 MB of
someone's memory, and `CGImageRelease` is still the only thing that returns them.

**One transient failure, recorded because a switcher will meet it.** In one run out of roughly ten, the
cold capture returned `SCK_TIMEOUT` — the completion handler never fired within 10 s — while a second
process was concurrently holding 60 full-res surfaces. The shareable window count had also dropped from
97 to 72. It did not reproduce on retry. **The timeout is not decoration: SCK can simply not answer**,
and P2.6 must treat a capture as failable and time-bounded rather than assume a result.

**`sck_init` is mandatory and its absence is fatal, not recoverable.** A process that is not an
NSApplication never opens CoreGraphics' WindowServer connection, and the first `SCScreenshotManager`
call then dies on `Assertion failed: (did_initialize), function CGS_REQUIRE_INIT, CGInitialization.c:44`
— an `abort()`, with no error to handle. Any CG display call fixes it; `CGMainDisplayID()` is the
cheapest. `NSApplicationLoad()` also works but pulls in AppKit and a main-thread requirement that this
path otherwise does not have. This will bite again in P0.1/P0.2 and anywhere else a Go binary touches
CoreGraphics before AppKit is up.

**Weak-linking SCK needs an environment variable.** P0.6's task notes said to weak-link the framework as
AltTab does, so the binary still loads where ScreenCaptureKit is absent — Go forces `minos 11.0` (D1) and
SCK arrives in 12.3, so this is a real gap and not a formality. cgo rejects it: both
`-weak_framework ScreenCaptureKit` and `-Wl,-weak_framework,ScreenCaptureKit` fail the LDFLAGS allowlist
with `invalid flag in #cgo LDFLAGS`. It does work with `CGO_LDFLAGS_ALLOW='-Wl,-weak_framework.*'` in the
environment, confirmed by `otool -L` showing the framework marked `weak`. **That is a build-script
requirement, not a source-level one:** `scripts/build.sh` must set it before Phase 2 ships anything, and
no plain `go build` of that package will be correct without it. The spike links hard, so that the
`go run ./spike/sck` in AGENTS.md keeps working.
