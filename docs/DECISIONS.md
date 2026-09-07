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

## D13 · P0.1: the panel is not the problem — 1.3 ms warm, 14 ms cold — 2026-09-06

`spike/panel`, macOS 26.6.2, M-series, 3024x1964 Retina, 8 tiles drawn in one view. Three runs of 30
warm summons each agreed to within ~1 ms; the numbers below are representative rather than best-case.

| | call returned | drawRect: ran | frame committed |
|---|---|---|---|
| cold (first ever summon) | 0.8-4.1 ms | 1.5-4.8 ms | **13.6-18.2 ms** |
| warm, mean | 1.1-1.3 ms | 0.8 ms | **1.3 ms** |
| warm, p50 | 0.8 ms | 0.4 ms | 0.8 ms |
| warm, worst of 30 | 5.5 ms | 5.2 ms | **3.4-5.9 ms** |

**Against a 100 ms budget the panel costs about 1% of it.** That is the headline, and it is the first
Phase 0 target that comes back with room to spare rather than a constraint. Contrast D12: capture is
~46 ms and cannot be done on the summon path at all. **The summon budget will be spent on data, not on
drawing** — which is a good place to be, because data is the part we control.

**"The call returned" is not "the frame is on screen", and the gap is the whole cold cost.** On the
first summon `orderFrontRegardless` returns in ~1 ms while the frame does not commit for another ~13 ms.
ALTTAB-LESSONS section 5 predicted this — CoreAnimation commits at the end of the runloop turn — and it
is why the spike reports three timestamps instead of one. Anything that measured the call returning
would have reported 1 ms cold and been wrong by an order of magnitude.

**Every warm summon really re-renders.** `drawRect:` was instrumented specifically to rule out the
alternative reading, that a re-ordered window is served from a cached backing store and the warm number
is therefore measuring nothing. **0 of 30 warm summons skipped the draw**, in all three runs. The 1.3 ms
is the cost of actually drawing eight tiles.

**Two honest limits on these numbers.**

- `commit_ms` and `turn_ms` came out *identical to two decimals* on every single sample. The
  `CATransaction` completion block and the following main-queue block run in the same runloop drain,
  microseconds apart. So what is measured is "the end of the runloop turn in which the panel was ordered
  front", which is when CA hands the frame to the render server — **not photons**. Add up to one display
  refresh (8.3 ms at 120 Hz, 16.7 ms at 60 Hz) for the real thing. Even the pessimistic reading of the
  worst cold sample is ~35 ms, still a third of the budget.
- The spike drives a **nested run loop from Go**, which is the opposite of the app's threading rule
  (ARCHITECTURE.md#threading: the thread is AppKit's forever, Go marshals back). That is fine for
  measuring and wrong for building; P3.1 must not copy the structure, only the numbers.

**The non-activating panel works.** Style mask `NSWindowStyleMaskBorderless |
NSWindowStyleMaskNonactivatingPanel`, activation policy `Accessory`, `becomesKeyOnlyIfNeeded`, shown with
`orderFrontRegardless`: across 20 summons the frontmost application's pid **never changed** (checked
against `NSWorkspace.frontmostApplication` before and after each). A switcher that deactivates the app
you are switching away from is broken, and this is the combination that avoids it.

**Not verified, and it needs a human.** `collectionBehavior` is set to `canJoinAllSpaces |
fullScreenAuxiliary | Stationary`, which is what should make the panel follow the user across Spaces and
appear *over* a full-screen app rather than behind it. Neither was actually tested — entering full-screen
and switching Spaces cannot be driven synthetically here (TCC blocks synthetic keystrokes, the same wall
P0.7 hit). **The settings are prior art from AltTab, not a measurement**, and they are recorded as such.
This is the one open question P0.1 leaves behind, and full-screen is where switchers most often fail.

---

## D14 · P0.4b RESOLVED — thumbnails release cleanly, and the instrument is proven, not assumed — 2026-09-06

`spike/sck -cycles 100`, macOS 26.6.2, 9 shareable windows rotated through, 400 px tiles. Every numbered
sample is taken with **zero images held**, so anything left in the IOSurface row is a leak.

| | IOSurface virtual | footprint |
|---|---|---|
| cycle 0 → 100, including framework warm-up | **+0.0 MB** | +2.3 MB |
| cycle 10 → 100, steady state | **+0.0 MB** | +0.4 MB |

100 cycles, 0 captures failed. **P0.4b's criterion — no growth across 100 capture/release cycles — holds.**
The ~2 MB of footprint drift is the Go heap and SCK's XPC machinery coming up over the first ten cycles,
not surfaces: the surface row never moves off zero at all.

**The zeros are only worth something because of the control, and that is the actual finding here.** A row
that reads 0.0M on every sample is what a clean release looks like *and* what a blind instrument looks
like — the two are indistinguishable from the deltas alone. That is not a hypothetical worry: D12's whole
result was that P0.4a's instrument watched `CG raster data`, which SCK output never touches, and reported
a leak-free 7 MB process that was holding 330 MB. So the run ends by holding 20 thumbnails deliberately:

    control: 20 images held, 8.3 MB declared — IOSurface +8.5 MB, then -8.5 MB on release

The row tracks declared bytes to within 2%, and gives all of it back. The instrument can see what it is
being asked to watch, so "no growth" now means no growth. A run whose control does not move the row
prints INCONCLUSIVE and says why, rather than a PASS it has not earned.

**Practical consequence for P2.6:** `CGImageRelease` on the CGImage SCK hands back is sufficient — there
is no separate `IOSurfaceDecrementUseCount` step to remember, and no accumulation across a hundred
captures. The bound on thumbnail memory is therefore entirely P1.7's cache policy's to enforce; the
platform will not leak underneath it. What is still unmeasured is *held* steady state at realistic
scale — 8.3 MB for 20 tiles at 400 px is the shape, but a 50-window cache at Retina resolution is
P2.6's number to take, against this same instrument.

**Sampling from inside the process is required, not a convenience.** A capture/release cycle is ~50 ms;
an external sampler cannot be told where a cycle boundary is and would read the middle of one. `spike/sck`
therefore carries its own copy of the `vmmap --summary` parsing that `spike/procmem` has. Three copies of
that parser now exist (memprobe, procmem, sck) and that is deliberate — spikes are throwaway probes, not
a library. The moment a non-spike needs it, it becomes one package and these die with the phase.

---

## D15 · P0.5 — Phase 0 closes with three answers and one debt — 2026-09-06

Phase 0 existed to settle the platform unknowns that would change the design *before* Phase 2 was planned
against them. It earned that framing twice: **D3** killed `CGWindowListCreateImage` and forced
ScreenCaptureKit, and **D12** showed capture cannot happen on the summon path at all. Neither was
predictable from reading documentation. This is the write-up P0.5 asked for.

**What is settled, and what Phase 2 may now assume:**

| unknown | answer | where the design moved |
|---|---|---|
| Can Go put a switcher panel on screen fast enough? | **yes — 1.3 ms warm, 14 ms cold** (D13) | drawing is ~1% of the summon budget. It is not where the time goes. |
| Can Go capture thumbnails on modern macOS? | **yes, but ~46 ms warm / ~112 ms cold, and it does not parallelise** (D12) | P2.6 captures *ahead* of summon; P3.1 must render a tile with no thumbnail yet. This is a launch requirement, not a refinement. |
| Do the bitmaps come back? | **yes — 100 cycles, `IOSurface` +0.0 MB, with a control** (D14) | `CGImageRelease` is sufficient. The bound on thumbnail memory is entirely P1.7's policy to enforce; the platform will not leak underneath it. |
| Does the hotkey arrive in time? | **unknown** | nothing. P0.2 is code without a number. |

**Where the 100 ms summon budget goes** is the single most useful thing Phase 0 produced, and it is not
the answer the design started with. Enumeration is ~46 ms cold / 0.30 ms warm (D5), capture is ~46 ms and
refuses to parallelise, drawing is ~1.3 ms. **The budget is spent on data, not on pixels.** A design that
captured on summon was never going to fit, and it was drawn that way until D12.

**The debt is P0.2 and it is recorded as a debt, not rounded up.** The tap installs at the head of the
session queue, swallows the key, handles `kCGEventTapDisabledByTimeout`, and instruments the C→Go
crossing on the tap's own CoreFoundation thread — the crossing D1 measured at 39 ns was on a thread the
Go runtime already owned, and a tap callback is not that. What is missing is delivery latency, and it is
missing for a reason that no amount of further work removes: **a synthetic event's timestamp is stamped
by the poster, not by the HID path.** `spike/hotkey` proves this the hard way — subtracting the two
clocks produced a delivery latency of *-12.7 days* drifting by ~40,667 ms per second of uptime, the
signature of the 41.667 ns/tick mismatch between `mach_absolute_time` ticks and `CGEventGetTimestamp`
nanoseconds. That bug is fixed; the deeper problem is not a bug. Measured synthetically the number is
post→tap routing only, which is a lower bound on delivery and not delivery. The spike therefore prints
`INCONCLUSIVE` and asks for `-manual`.

Phase 0's own rule (AGENTS.md) is that an honest `INCONCLUSIVE` beats a green light, and the phase
already produced one false `GATE PASS` that had to be retracted. So P0.2 stays `[~]` and moves to
**V6.1**. Phase 2 proceeds on the assumption that ⌥⇥ arrives inside 5 ms — tagged as an assumption in
the roadmap, at the task that depends on it.

**Phase 0's honest scorecard: it was worth it.** Four tasks changed the design (D3, D5, D12, D13), one
was dropped after its premise collapsed (D10, P0.7), and one produced a retraction (D4 → D8). The
retraction is the evidence the phase was doing its job rather than confirming a plan.

---

## D16 · Testing deferred to a Phase 6 — 2026-09-06

**Decision:** per-task tests and measurements are no longer written alongside the work. Phases 2–5 are
done when the code is written, `scripts/check.sh` is still green on what already exists, and the roadmap
box is `[x]`. Everything that would have been verified in place is collected in **Phase 6** and run once
against the assembled app.

**Why it is defensible here.** Most of what would have been tested in Phases 2–3 cannot be unit-tested
anyway. `ARCHITECTURE.md` already rules `internal/platform` out of unit testing — it is a humble object
verified by spikes — so "defer the tests" in Phase 2 largely means deferring *manual verification runs*,
which were always going to be a batch at the end. Phase 1, the part that is genuinely testable, is done
and its suite is green. Batching the rest against a running app also tests the thing that actually
matters: seven subsystems composing, which no per-task test observes.

**Why it is a real cost, stated plainly rather than talked out of.** This project's whole Phase 0 thesis
is that measurement changes the design, and it was right twice (D3, D12). Deferring verification means
Phases 2–5 are built on four assumptions nothing has checked:

1. the hotkey arrives inside 5 ms (V6.1) — the summon path is designed around it
2. the panel behaves over a full-screen app and across Spaces (V6.2) — prior art, never measured (D13)
3. a 50-window Retina cache stays inside a sane bound (V6.4) — D14 measured 20 tiles at 400 px
4. summon → pixels stays under 100 ms once real data flows (V6.3)

A negative in Phase 6 sends work back into Phase 2 or 3 rather than being absorbed locally, and that
rework is the price being paid for the speed gained now. **The mitigation is visibility, not optimism:**
each of the four is tagged `assumption` in `docs/ROADMAP.md` at the task that depends on it, pointing at
the V6 task that settles it. An assumption written down at its point of use is recoverable; one carried
in someone's head is what makes the rework expensive.

**What did NOT change.** `scripts/check.sh` stays the merge gate and stays green — deferring means
writing no *new* per-task tests, never deleting the suite that exists or letting the gate go red. The
rule against reporting a gate as passing on a number you don't believe is untouched, and P0.2 is the
first thing it is applied to under this policy: it stays `[~]`.

---

## D17 · cgo moves the deployment target, and D2's macOS 11 floor is now 12 — 2026-09-06

Two corrections, found by P2.1 the first time an Objective-C file existed in a non-spike package. Both
were invisible until then, and one of them shipped a `.app` that could not launch on the OS it claimed.

**Measured on this host (macOS 26.6.2, Go 1.26, SDK 26.0), same trivial `main`:**

| build | `minos` |
|---|---|
| pure Go, no cgo | 12.0 |
| cgo enabled, no C compiled | 12.0 |
| **cgo with a real `.m` file** | **26.0** |
| cgo with a real `.m` file, `MACOSX_DEPLOYMENT_TARGET=12.0` | 12.0 |

**Correction 1 — D2 is stale.** D2 recorded that Go's linker forces `minos 11.0`. On Go 1.26 it forces
**12.0**. Nothing in the project's own code moved it; the toolchain's floor rose underneath a number that
was written down once and then treated as permanent. README, `ARCHITECTURE.md`, `install.sh`'s guard and
`Info.plist` all said 11 and are now 12. D2 stands as what was true when it was measured; this supersedes
it. The `spike/sck` reasoning survives unchanged — SCK arrives in 12.3, still above the floor, so it must
still be weak-linked.

**Correction 2, and the one that matters — once real Objective-C is compiled, clang decides the
deployment target, not the Go linker,** and clang's default is the SDK's own version. The `.app` this
produced declared `LSMinimumSystemVersion 11.0` in its plist and carried `minos 26.0` in its load
commands. Nothing warns about that. It builds, it signs, it runs perfectly on the machine that built it,
and it refuses to launch on anything older than macOS 26 — a failure that only ever appears on someone
else's computer.

**Fix, in `scripts/build.sh`:** one `MIN_MACOS` variable feeds both `MACOSX_DEPLOYMENT_TARGET` and the
plist, so the two cannot drift, and the build **fails** if any slice's `minos` disagrees with the plist:

    verifying deployment target
      x86_64: minos 12.0
      arm64: minos 12.0

The check is the actual deliverable. This regression is one line away at all times — a new
`-framework`, an Xcode update, a CI runner image bump — and the whole reason it cost anything is that it
is silent. A build that fails loudly on the developer's machine is worth more than the correct value
committed once.

**What is still unverified:** that a 12.0-targeted binary actually *runs* on macOS 12. Nothing here has a
macOS 12 machine, and per D16 that is V6.7's to establish. The claim being made today is narrower and is
the one that was wrong before: the binary and its plist now agree.

**Incidental:** the ~14 `ld: warning: object file was built for newer 'macOS' version` warnings that
appear when lowering the target are stale build-cache artifacts, not a real conflict. They vanish on a
clean `go build` and are not worth suppressing — suppressing them would hide the real version conflict
they exist to report.

---

## D18 · `cmd/gotab` was never in the repository — 2026-09-06

Found while committing P2.1, because `git status` did not list a file that had just been rewritten.

`.gitignore` carried an unanchored `gotab`, intended for the binary `go build ./cmd/gotab` drops in the
repo root. A gitignore pattern without a slash matches **any path component** with that name, at any
depth, directories included. So it matched `cmd/gotab/` — the project's only `package main`, which has
therefore never been committed.

**A fresh clone had no `cmd/` directory at all**, and `scripts/build.sh` failed on it:

    ==> Building GoTab 0b260c4
        compiling arm64
    stat /tmp/.../cmd/gotab: directory not found

`.github/workflows/ci.yml` runs `check.sh` then `build.sh`, so CI has been failing on every commit this
repository has ever had. Nothing surfaced it, because nothing here reads CI.

**Why it hid so well.** Every local check passes: the file is on disk, `go build ./...` and
`scripts/check.sh` are green, `./scripts/build.sh` produces a working `.app`. `go build ./...` is green
in the *clone* too, since a package that does not exist cannot fail to compile. The only signal was the
absence of a line in `git status` — and an earlier session read the same symptom as "a broken build
(`cmd/gotab` was empty)" and fixed the contents rather than the tracking.

**Fix:** anchor both patterns — `/gotab` and `/spike/spike` — and commit the package. The anchoring is
the point: an unanchored name in `.gitignore` is a claim about every directory in the tree, and this repo
has a `spike/` directory whose subdirectories are named after what they probe.

**What this says about the gate.** `scripts/check.sh` is thorough about the things it was pointed at —
formatting, vet, tests, the `core` purity invariant — and structurally could not see this, because it
runs against the working tree rather than against what the working tree would produce. That is a gap in
what "green" means, not an argument for a bigger checklist. **V6.7 already builds from a clean state and
is where this belongs**; it now has a concrete failure it must catch, rather than a hypothetical one.

---

## D19 · CGWindowList over-reports switchable windows by ~8x — 2026-09-06

P2.2's first real run, on an ordinary session with Screen Recording granted:

| | count |
|---|---|
| `CGWindowListCopyWindowInfo`, `kCGWindowListOptionAll` | 86 |
| with `ExcludeDesktopElements`, all layers | 82 |
| after dropping `kCGWindowLayer != 0` | **59** |
| of those, carrying a `kCGWindowName` | **7** |
| of those, `kCGWindowIsOnscreen` | 5 |

Counts are one snapshot of one session and drift by a few as windows open and close; the ratio is the
finding, not the digits. `ExcludeDesktopElements` earns almost nothing on its own (86 → 82) — the layer
filter is what does the work.

The seven titled windows were exactly the seven a user would expect in a switcher: two Chrome windows,
System Settings, Notion Calendar, Docker Desktop, GoLand, Activity Monitor. The other 52 were XPC view
services (`CursorUIViewService`, `AutoFill`, `WidgetControlViewService`), `loginwindow`, thumbnail
extensions, and several untitled auxiliary windows per application.

**Layer 0 is necessary and nowhere near sufficient.** The filter is worth keeping — it removes a third
of the list and everything it removes is genuinely not a window — but a switcher built on `CGWindowList`
alone would show its user roughly eight entries of noise for every real one.

**The title is a far better discriminator, and it is still the wrong one to build on.** It is empty for
all of these on a machine without Screen Recording (P2.2 handles that case explicitly), and a real
window is allowed to have no title — a fresh untitled document is the obvious example. Something that
works on this machine today and shows nothing on a machine without a TCC grant is not a filter, it is a
coincidence.

**Consequence: P2.3 is load-bearing, not an enhancement.** Accessibility's `kAXWindowsAttribute` per
application is what actually enumerates switchable windows, which is why AltTab is built on AX and uses
`CGWindowList` only for identity and geometry. The roadmap had P2.3 as "AX observer registration;
callbacks enqueue only" — observation. It also owns *enumeration*, and P2.2's output is the candidate
set it filters, not the window list.

**What did not change.** P2.2's contract stands: one crossing, packed array, no per-window call. That
discipline is what makes an 8x over-count merely wasteful rather than expensive — 59 records cost one
crossing, and the 52 that get discarded cost nothing but the copy. Filtering earlier, in C, would have
meant encoding a switchability policy in the layer that is meant to report facts.

---

## D20 · AX is the right filter and the wrong list — 2026-09-06

P2.3a built the Accessibility enumeration D19 called for, and it works: **5 switchable windows against
CGWindowList's 59 layer-0 candidates**, and all five carried a `CGWindowID` that CoreGraphics also
reported. That last number is the one that matters most, because the only reason the AX list has an ID
at all is the private `_AXUIElementGetWindow` — 5 of 5 agreeing with a public API is evidence the
private symbol returns real window numbers rather than plausible ones.

**Then the same run showed AX missing two windows CoreGraphics could see**, and the two misses have
different causes:

| window | AX | CoreGraphics | cause |
|---|---|---|---|
| Chrome, "Facebook" (id 45) | absent | titled, `IsOnscreen` false | `kAXWindowsAttribute` returned **1** window for a process that has 2 |
| Notion Calendar (id 1631) | absent | titled | `activationPolicy == .Accessory`, **and** `kAXWindows` returns 0 for it regardless |

Probed directly to separate the two rather than guessing:

    Google Chrome    pid=1079  policy=0  kAXWindows err=0 count=1
      id=2859  min=0  New chat - Claude - Google Chrome
    Notion Calendar  pid=31226 policy=1  kAXWindows err=0 count=0

Both calls **succeeded**. This is not an error path, a timeout, or a missing grant — AX answered, and
its answer was short.

**Conclusion, and it changes the design: neither enumeration is correct alone.** CoreGraphics sees every
window and cannot say which are switchable; Accessibility says which are switchable and cannot see every
window. The model has to be built from a **join** on `CGWindowID`, not from either list — CoreGraphics
supplying the universe and the on-screen state, AX supplying switchability, title and minimized state.
That is a new task, **P2.3c**, and it is not optional: shipping P2.3a's list alone means a switcher that
silently cannot reach a window on another Space, which is a headline feature.

**What is not established.** *Why* Chrome's second window is invisible to AX. Another Space is the
likeliest explanation and matches the received wisdom about `kAXWindowsAttribute`, but nothing here
confirms it — driving a Space change needs a human (TCC blocks synthesising it, the same wall P0.1 and
P0.7 hit). It is recorded as unconfirmed and belongs with **V6.2**, which already has a human in front of
a full-screen app and multiple Spaces. **P2.4's SkyLight Space query is what turns the guess into a
number**, and it is now load-bearing rather than a refinement.

**The `.Accessory` filter stays**, despite Notion Calendar proving accessory apps own real windows.
Removing it recovers nothing — AX reports zero windows for that process either way — and it would add
~50 processes of Mach IPC per enumeration. The gap is real and P2.3c's join is where it gets closed.

**Cost is unmeasured**, per D16. AX enumeration is Mach IPC per attribute per window, which is a very
different order from D1's 31 ns cgo crossing, and D5's launch-time budget is what it has to fit in. Each
application element carries a 0.25 s messaging timeout so a wedged app is skipped rather than waited on,
but no timing number is claimed here. **V6.3** owns it.

---

## D21 · The join works, and it costs a TCC grant — 2026-09-06

P2.3c joins both enumerations on `CGWindowID`. Measured on the same session as D19 and D20:

| | count |
|---|---|
| CoreGraphics layer-0 candidates | 58 |
| Accessibility switchable set | 5 |
| **joined result** | **7** |
| excluded, activation policy `prohibited` | 13 |
| excluded, no title and AX did not report it | 37 |
| excluded, process already gone | 1 |
| **titled windows excluded** | **0** |

The seven are the five Accessibility reported plus **both of D20's misses** — Chrome's second window and
Notion Calendar's — recovered. Five carry `both` provenance, two carry `cg`. No titled layer-0 window is
excluded, which is P2.3c's contract discharged rather than asserted.

**The admission rule is `activation policy is not prohibited` AND `the window has a title`. Both halves
are needed and neither is sufficient**, which was worth finding out by trying the weaker one:

- **Policy alone admits 37 untitled auxiliary windows** — offscreen buffers, popovers and toolbars
  belonging to Chrome, Finder, Terminal, GoLand and System Settings. Real applications own a great many
  layer-0 windows nobody can switch to. A first attempt shipped this rule and produced a 44-window list.
- **Title alone** admits any XPC view service that happens to have one, which is what the policy check
  is there to stop.

**The uncomfortable part: this rule depends on a TCC grant.** `kCGWindowName` requires Screen Recording.
Without it every CoreGraphics-only title is empty, the recovery branch admits nothing, and the result
degrades to exactly the Accessibility list — losing the other-Space windows the join exists to recover.
D19 already warned the title is not a filter and this is that warning coming true, only narrowed: the
title is not the *primary* filter, it is the tiebreak for windows AX could not see, and the failure mode
is a list that is short rather than a list that is wrong.

That degradation is reported rather than hidden — `Enumerator.MissingRecovery` is true exactly when it
applies, and `gotab -list` says so. A user who grants Screen Recording for thumbnails gets correct
enumeration as a side effect, which is worth knowing when P4.3 writes the onboarding copy: the grant is
not only about pictures.

**The lead worth following, and deliberately not followed here.** `kCGWindowBounds` needs no grant at
all, and the 37 wrongly-admitted windows are plausibly separable by size — many auxiliary windows are
tiny or zero-area. If bounds discriminate as well as the title does, the Screen Recording dependency
disappears from enumeration entirely. That is a measurement, not a guess, and it is not this task's:
recorded here so the next person does not have to notice it independently.

**Cost is two crossings, not 2N** — one enumeration per source regardless of window count, which is the
cgo rule holding up under a design that reads the window list twice.

---

## D22 · Posting to the event loop must never block, and dropping is sometimes correct — 2026-09-06

`internal/app` exists (P2.7). The design question it had to settle is what a platform callback is
allowed to do, because `ARCHITECTURE.md#the-cgo-rule` says "enqueue and return" and a channel send is
not automatically either of those things.

**A blocking send in a callback is a bug with a name.** An Accessibility callback or a `CGEventTap`
callback runs on a thread the Go runtime has never seen, and the system disables a tap whose callback is
too slow — `kCGEventTapDisabledByTimeout`, which P0.2's spike handles explicitly because it is the
documented failure mode. A hotkey that dies silently after one stall is the worst outcome available. So
`Post` is a non-blocking select with a `default`, always.

**That forces a decision about a full queue, and the answer differs by event.** Two mechanisms, not one:

| | mechanism | full behaviour |
|---|---|---|
| discrete events (Focused, Summon, Cycle, Quit) | buffered channel, depth 256 | drop, and report it to the caller |
| "the window set changed" | channel of depth 1 | drop, **and that is correct** |

The second is the interesting one. A dropped rescan signal loses nothing, because the signal already
pending means "re-read everything" — twenty Accessibility notifications arriving during a Space switch
coalesce into one enumeration, which is the behaviour wanted rather than a compromise. Depth 1 is not a
small buffer, it is a latch.

For discrete events a drop is real loss, so `Post` returns whether it was accepted and the depth is
generous. `Focused` is survivable — the next rescan repairs ordering — and `Summon` and `Quit` are not,
so their callers check.

**Re-enumeration must not disturb MRU order, and that turned out to be an argument value.** `doRescan`
touches every window on every pass. It upserts with a **zero `FocusSeq`**, which `Model.Upsert` reads as
"preserve what you have" — so a window whose title changed keeps its position. ALTTAB-LESSONS §3's rule
that a title change may not reorder is enforced by passing zero, not by a branch. The order is rebuilt
only when membership actually changed, because a rebuild *is* a reorder.

Observed: 12 rescans over 6 seconds produced **one** state print — the order did not churn. Note what
that does and does not show. It shows repeated enumeration is stable; it does not exercise a title
changing mid-run, which was not observed. That case is pinned in core by
`TestModelUpsertZeroFocusPreserves`, and the swap-with-last removal the loop's backwards delete depends
on is pinned by `TestModelRemoveMiddleFixesMovedRow`. Both predate this task.

**A failed rescan does not stop the loop.** The WindowServer declines during fast user switching and at
the login window, and a TCC grant can be revoked mid-session. Those are ordinary states, not faults; a
loop that exited on them is a switcher that stops working and never says why. They go to `OnError`.

**No mutex, and that is load-bearing rather than stylistic.** One goroutine reaches the model. If a
second one ever needs to, the fix is to move that caller onto the loop, not to add a lock — AGENTS.md
says to say so rather than adding the mutex, and this is the package where that would be tempting.

---

## D23 · Deferred verification is the standing rule, not a Phase 2–5 exception — 2026-09-07

**Decision:** D16 deferred per-task tests and measurements for Phases 2–5 specifically. That scope is
removed. **No task in any phase writes tests as it goes; all verification accumulates in the final
phase** — Phase 6 today, and whatever the last phase is if more are added. A new phase does not bring
its own test burden with it; it brings more rows to that table.

**Why generalise rather than re-decide it each phase.** The rule was stated in three places
(`AGENTS.md`, `ARCHITECTURE.md`, `PARALLEL-WORK.md`) and all three said "Phases 2–5". A phase-scoped
rule expires silently: the session that opens Phase 3 has to notice the scope, decide whether it still
applies, and update three files that will otherwise disagree — which is the drift AGENTS.md exists to
prevent. The reasoning in D16 was never specific to Phases 2–5 anyway.

**What this buys, stated as the reason it was asked for:** time and tokens. Writing a test beside each
Phase 2 task costs roughly as much as the task, and for `internal/platform` it buys almost nothing —
ARCHITECTURE.md already rules that layer out of unit testing, so a per-task test there would mostly be
a mock asserting that the code calls the API it obviously calls. Batching also tests the thing that
actually matters: subsystems composing, which no per-task test observes.

**What did NOT change, and these are what keep the deferral honest rather than merely cheap:**

- `scripts/check.sh` stays the merge gate and stays green. Deferring means writing no *new* per-task
  tests; it never means deleting the suite that exists or letting the gate go red.
- A decision a deferred test would have caught is tagged **`assumption`** in `docs/ROADMAP.md` at the
  task that depends on it, pointing at the verification task that settles it.
- A box is never `[x]` on a number nobody believes. P0.2 remains the worked example: complete code, no
  measurement, deliberately still `[~]`.

**The cost is unchanged from D16 and is not being talked out of.** A negative in the final phase sends
work back into an earlier one rather than being absorbed locally. D16 listed four assumptions riding on
this; P2.4 has since retired part of one and P2.6 has added two more (weak-linking, and a live-image
counter with no producer). The mitigation is visibility — an assumption written down where it is used
is recoverable; one carried in someone's head is what makes the rework expensive.

---

## D24 · P2.4: D20's "it must be on another Space" does not hold — 2026-09-07

`core.SpaceID` is now populated by `CurrentSpace()` / `SpacesOf()` / `Spaces()` (`space.{h,m,go}`).
SkyLight is reached through `dlopen`/`dlsym`, not a linked private framework, so `scripts/build.sh` is
untouched and a moved symbol degrades one field to "unknown" rather than failing the launch.

**Measured on a live session, both TCC grants held, five runs:**

| | |
|---|---|
| `Spaces()` | `[1]` — one display, one Space, `id64 = 1` |
| `CurrentSpace()` | `1`, stable across 5 consecutive calls, every run |
| CoreGraphics layer-0 candidates | 54–63 |
| Accessibility switchable set | 5–7 |
| **windows on a Space other than the current one** | **0, every run** |
| `SpacesOf` over 54–63 ids | 1.4–4.8 ms total, 22–87 µs/window, one crossing |

**D20 guessed** the Chrome window Accessibility failed to enumerate was on another Space. It was not:
both AX-invisible windows (an untitled Chrome window and Claude's) sat on Space 1 — the Space the user
was looking at — on a machine with no second Space to be on. A window can be invisible to Accessibility
while on the current Space, so D20's guess is not a general rule.

**Still open, needs a human.** With one Space there is no way to confirm the converse — that a window
genuinely on a second Space reports a `SpaceID != CurrentSpace()`. The negative is established; the
positive is V6.2's, which already has a human in front of multiple Spaces.

`0` stays "unknown" and is not a silent default: `SpacesOf([999999 1])` returns `[0 1]`, so the
WindowServer declines rather than falling back to the current Space. `Rules.CurrentSpace = 0` must keep
meaning "do not filter by Space" — 46–54 of 63 windows report 0, and a filter that read unknown as
off-Space would empty the switcher.

**A lead, not followed:** `CGSCopyWindowsWithOptionsAndTags` inverts the query to one round trip per
Space, but disagreed with `SpacesOf` on exactly the two windows AX also could not see — a difference of
meaning ("assigned to" vs "ordered in on"), and validating it needs a multi-Space machine.

---

## D25 · P2.5: raising a window is two independent halves — 2026-09-07

`Raise` / `Minimize` / `Unminimize` / `Close` (`action.{h,m,go}`) turn the platform layer from an
enumerator into a switcher. There is no public lookup from a `CGWindowID` to an `AXUIElement`, so each
asks CoreGraphics for the owning pid and searches only that process, with a fallback scan of every
regular application for windows AX knows and the WindowServer's list does not (D20). A stale id costs
~24 ms and returns `ErrNoWindow`, never a panic — that race is the normal case for a list that was out
of date when it was drawn.

**Measured on macOS 26.6.2, four trials per variant:**

- **`kAXRaiseAction` never answers during the Dock's restore animation.** A just-unminimized window
  burns the full messaging timeout and returns `kAXErrorCannotComplete` (−25204) — 251–255 ms, against
  0.2–0.4 ms for the unminimize and the attribute writes around it. The action still lands; only the
  reply is missing. That one call now gets a 20 ms timeout and reads `kAXErrorCannotComplete` as
  success: same 4/4, 28–35 ms instead of 258–292 ms.
- **Restoring from the Dock does not raise the window inside its app.** Skipping the raise for a
  just-unminimized window is the obvious way to dodge the timeout above and is wrong: 0/4 put the
  window at the front of its app, though 4/4 still made the app frontmost. The two halves of a switch
  are genuinely independent.
- **Activation must not hang off the raise.** The first version returned early on a failed raise, so
  every minimized window hit the timeout and then never activated — the user watched the window
  un-minimize behind whatever they were leaving. `activate_app` now runs regardless of what the raise
  returned.

**`CGWindowListCopyWindowInfo` reports already-closed windows.** Finder windows closed through this API
stayed in the CG list — and so in P2.3c's join — while AX had already dropped them. `Close` is correct
(3/3 against the AX count); this is why resolution never trusts CoreGraphics past the pid.

**`GT_ERR_NO_WINDOW = 6`** extends `shim.h`'s frozen status enum from `action.h`; `action.go`'s
`actionError` maps it, the same shape as `shim.go`'s `statusError` for the other five. A later task
adding a status of its own must reconcile the two numbering spaces — the closed enum is still the
design.

**Not verified (assumption → V6.5):** the `ErrNotTrusted` path (the terminal holds the grant and
revoking it would cost the session) and `ErrUnavailable` from `Close` on a window with no close button
(none was available). Both branches are written; neither is run.

---

## D26 · P2.6: capture confirms D12, and the live counter had no producer — 2026-09-07

`Capture(id, maxWidth)` (`capture.{h,m,go}`) grabs one window through ScreenCaptureKit, downscaled in
`SCStreamConfiguration` at capture time — the full-res bitmap is never allocated. `64/200/400/800` px
bounds produce exactly `64×42 / 200×130 / 400×263 / 800×527`; anything above the window's own size
clamps rather than upscales.

**Measured, macOS 26.6.2, 65–77 layer-0 windows on screen:**

- **Cost: 193 ms cold, 57 ms warm, 56.7 ms mean over 100 cycles.** D12's ~112/~46 was a quieter
  machine; the shape holds and the conclusion stands — **capture never runs on the summon path.**
- **Release is clean, and the instrument was shown able to see it.** 100 cycles with no images held:
  `IOSurface` +0.0 MB. The run then ends holding 14 thumbnails on purpose: +2.3 MB against 2.1 MB
  declared, all given back on release — D14's control, so the zero is a measurement not a blind gauge.
- **The timeout fires and leaves nothing behind.** Forced with a 1 ms bound against a warm cache:
  40/40 returned `ErrTimeout`, slowest call 21 ms, all 40 abandoned completion handlers fired later
  into a context nobody waited on with no crash and `IOSurface` at 0.0 MB. This is why the capture
  context is heap-allocated and reference-counted rather than `__block` on the stack.
- **A 2–10% ordinary failure rate a caller must expect.** Windows `CGWindowList` reports and SCK will
  not capture, or that closed during the run; retrying twice recovered none. Mapped to `ErrUnavailable`
  (a normal event for a prefetcher), not `ErrInternal` (which must keep meaning "a bug in this shim").
- **Concurrency is safe but pointless** — SCScreenshotManager serialises in the WindowServer (D12), so
  a prefetcher uses one goroutine, not a pool. **A stale cache entry never self-heals**: the
  shareable-window list refreshes on a miss only, so a closed-but-cached window fails every capture
  until something else forces a refresh.

**The live counter had no producer side.** `gt_image_live()` reports `shim.m`'s `static
g_images_live`, and `gt_image_release` — the only function touching it — decrements. Every handle was
born uncounted, so `LiveImages()` read 0 while images were held and went negative after (measured −181
over a soak). A counter that only counts down is worse than none: V6.4's leak assertion would have been
written against it and passed. The integrator added `gt_image_adopt` to the frozen `shim.{h,m}` (P2.6
owns neither); `capture.m` adopts at the line it had marked. `LiveImages()` now reads 0 at rest, 1..5
as handles are taken, 0 after release, and 0 after a double release.

**No `runtime.SetFinalizer` backstop, and the reason is structural.** `Capture` returns `ImageRef` **by
value**, so there is no stable heap object to attach a finalizer to; attaching one to a local before
copying it out would free the bitmap while the caller's copy still points at it. A debug backstop needs
`ImageRef` handed out as a pointer, which changes a frozen type and the contract's signature.
ARCHITECTURE.md is unaffected — the finalizer was always a detector, never the mechanism.

**`scripts/build.sh` weak-links ScreenCaptureKit.** SCK arrives in macOS 12.3 and the floor is 12.0
(D17), so a hard link makes the bundle refuse to *launch* on 12.0–12.2 — a launch failure, not a
missing feature. `capture.go` carries both links behind the `gotab_weak_sck` build tag, and cgo
evaluates the tag before its LDFLAGS allowlist, so a plain `go build`/`test`/`vet` links hard and needs
no environment (the gate is unaffected). Only `build.sh` sets the tag and
`CGO_LDFLAGS_ALLOW='-Wl,-weak_framework.*'`; `otool -L` then shows `ScreenCaptureKit ... , weak)`.

**assumptions → V6.4:** a 50-window Retina cache staying inside a sane bound (D14 saw 8.3 MB for 20
tiles at 400 px — the shape, not the number); and the 2 s `captureTimeout` being headroom over the
cold capture, not a measured tail latency under load.

---

## D27 · P2.3b: AX observers, and the one thing they need a main run loop for — 2026-09-07

`StartObservers(onChange)` / `StopObservers()` (`observe.{h,m,go}`) register one `AXObserver` per
regular application for `kAXWindowCreated`, `kAXUIElementDestroyed`, `kAXFocusedWindowChanged`,
`kAXWindowMiniaturized` and `kAXWindowDeminiaturized`, and follow applications launching and quitting
via `NSWorkspace`. Every notification ends in one line — `goObserveChange()` — on a run loop this shim
owns, never the main one: that traffic must not sit in front of the summon path's pixels, and the main
loop may not be running when `StartObservers` is called. `cmd/gotab -watch` now rescans on events, with
a slow ticker demoted to a backstop.

**Measured on this machine, throwaway `package main`:**

- **`NSWorkspace` launch/terminate needs a running MAIN run loop; nothing else here does.** With
  `CFRunLoopRun` on the main thread, a launched app and its new windows are seen; with no main run loop
  anywhere they are **never** delivered — not late, not on another thread. `queue:nil` vs
  `[NSOperationQueue mainQueue]` changes nothing; it is the *posting* that needs the loop. The AX
  observers themselves fire normally without one. **`cmd/gotab` runs no main run loop until Phase 3**,
  so today the symptom is "sees the apps that were open when it started, and no others" — stated as a
  precondition in `observe.h` and the `StartObservers` doc, and covered by the backstop ticker.
- **A freshly launched app registers only *some* of the five, and drops the two that matter.** Against
  TextEdit one dispatch after its launch notification: `kAXWindowCreated`, `kAXWindowMiniaturized`,
  `kAXWindowDeminiaturized` succeeded; `kAXUIElementDestroyed` and `kAXFocusedWindowChanged` returned
  `kAXErrorCannotComplete`. A retry gated on "did anything register" would leave the app permanently
  half-observed, so the gate is "all five": a per-pid bitmask retried on `{0,250,500,1000,2000,4000}`
  ms, asking only for what is missing.
- **`kAXUIElementDestroyed` on the *application* element does report descendant windows** — closing one
  TextEdit document while the app stayed alive delivered it. It is also the noisiest of the five (13 in
  a row during one deminiaturize), which is what D22's depth-1 latch is for.
- **Background apps trickle `kAXFocusedWindowChanged` on an idle machine** (Docker Desktop, Finder, in
  pairs every few seconds), and **`NSWorkspace` reports every helper process** — every `osascript` is a
  launch and a terminate. `onChange` is therefore not rare and not evidence anything changed; terminate
  is filtered to pids actually observed or `.Regular` apps (2 spurious rescans per shell command → 0).
- **Leak measurement.** `leaks` over 20 `Start`/`Stop` cycles: zero `AXObserverRef`, zero
  `CFRunLoopSource` from the start. One leak in the first draft — the observer thread's run loop,
  `CFRetain`'d and never released (20 × `ROOT LEAK <CFRunLoop>`); now released in `gt_observers_stop`
  after `g_posting` (threads inside `run_on_observer_thread`) drains to zero. Down to one 32-byte
  system `xpc_date_t`.

**assumption → V6.9:** behaviour under an Accessibility grant revoked mid-run. `gt_observers_start`
checks `AXIsProcessTrusted()` up front but nothing re-checks, and what macOS does to a live
`AXObserverRef` on revocation is untested — it needs a human toggling System Settings during a run.
