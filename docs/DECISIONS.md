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
`minos 11.0`. Native Swift switchers ship `MACOSX_DEPLOYMENT_TARGET` as low as `10.14.4`; the Go
toolchain will not.

**Consequence:** macOS 10.14/10.15 are out of scope, permanently. Recorded as accepted at project start
(the "clean break" decision), not as a regression to fix.

## D3 · Window thumbnails require ScreenCaptureKit — 2026-09-06

`CGWindowListCreateImage` is **obsoleted in macOS 15** — the SDK marks it `unavailable`, so it is a
compile error, not a deprecation warning. Discovered when the spike failed to build.

**Consequence:** thumbnail capture must use ScreenCaptureKit, which is asynchronous and block-based.
From Go that means ObjC block trampolines and C→Go callbacks (D1), which is materially harder than a
synchronous call. **P2.6 is the highest-risk task in Phase 2** and should be prototyped before Phase 2
is planned in detail. Switchers that target an older floor link it weakly
(`-weak_framework ScreenCaptureKit`) precisely because of version skew; expect to do the same.

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
credible** — including any claim that it beats a comparable switcher.

## D5 · Window enumeration: 57 ms cold, 0.30 ms warm — 2026-09-06

`CGWindowListCopyWindowInfo` for ~18 windows in one cgo call. The cold cost is framework load plus a first
WindowServer round trip; the summon path only ever pays the warm cost.

**Consequence:** 0.30 ms is comfortably inside the 100 ms summon budget, so batched enumeration is viable.
But the 57 ms cold cost must be paid at launch, not on first summon — warm the WindowServer connection
during startup or the very first ⌥⇥ of a session blows the budget.

## D6 · Project framing — why this project exists in this shape — 2026-09-06

Recorded because the reasoning came out of a conversation and would otherwise be lost. These are settled
decisions, not open questions.

**Goals, in priority order:** (1) lower memory than a comparable native switcher, (2) learn Go
properly. Feature parity is a means, not the goal. (Goal (1) was later dropped — see D10.)

**Scope decisions and what they bought:**

| decision | consequence |
|---|---|
| macOS only | no cross-platform constraints; full AppKit/private-API surface stays in play |
| clean break — new bundle ID | no Keychain/license migration, no 25 preference migrations to port, no Sparkle continuity. Removed the majority of the original risk. |
| full app replacement in Go | accepted with the cgo costs in D1 measured and understood up front |

**Rejected, with reasons, so they are not relitigated:**

- *Go core + Swift UI shell* — the pure kernels are the ~14% of a full switcher that does no IPC, so
  they are precisely the code with the least to gain from a port. The ~86% that would benefit is the
  part Go cannot express.
- *Preserving macOS 10.14 support* — impossible, see D2.
- *Preserving Pro licensing* — dropped with the clean break. Note for any future reversal: a
  same-bundle-id build with a **different code signature** cannot read a Keychain item but *can*
  silently overwrite it. Same Developer ID cert + TeamID + bundle ID is the whole requirement, and it
  is all-or-nothing.

**An honest caveat on goal (1):** no comparable switcher's actual memory footprint was ever measured
— none was running during the analysis session. So "lower memory than a comparable switcher" then had
**no baseline**. See P0.7. Until both P0.4a (an instrument that works) and P0.7 (a number to beat)
existed, the primary goal was unfalsifiable — and it was dropped in D10 before either was pinned down.

## D7 · Prior art is captured, not inherited — 2026-09-06

`docs/PLATFORM-LESSONS.md` distils hard-won macOS window-switcher platform knowledge: the two-plane
architecture, the rule that only an attention decision or structural repair may move MRU, the 60 ms
settle, the macOS traps (synchronous XPC inside AppKit, TCC responsible-process rule, the capture
drain, the signal-mask hazard), and the observability ceilings that cannot be engineered around.

Read it before designing any subsystem. It is the cheapest way to avoid re-deriving several years of
reverse-engineering. Every entry is a platform fact, presented on its own terms — nothing in this
project depends on any other codebase.

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

**macOS already evicts idle thumbnail pages.** A common design retains one `CALayerContents` per
window indefinitely, and GoTab planned to beat that with a bounded LRU. But the OS is already doing a
form of that eviction for free: an untouched 366 MB of CG raster data sat at ~6 MB resident without
any policy from us.

So a bounded LRU would reduce *virtual* size and *peak* footprint, but the steady-state resident win over
"retain everything and let macOS reclaim" may be small. It is still worth doing — peak footprint is real,
eviction under pressure has a latency cost when pages fault back in during a summon, and unbounded growth
is a genuine risk with many windows — but **the size of the win is now an open question, not a given.**

**This raises the stakes on P0.7.** Until a comparable switcher's real footprint is measured with D8's
instrument, we do not know whether the headline goal has meaningful room in it. Do P0.7 before designing the cache.

## D10 · Memory parity with another switcher is no longer a goal — 2026-09-06

**Decision (owner's call).** The project's goal is feature parity with the core switching a keyboard
window switcher is expected to do, written in Go. Using less memory than a comparable switcher is
dropped as an objective. D6's framing — "the reason this project exists is lower memory" — is
superseded; it is left in place because this file is append-only.

**Consequences**, all applied in the same commit:

- P0.7 dropped. Phase 0 is now de-risking (panel, hotkey, ScreenCaptureKit), not a go/no-go.
- The gate criterion "steady-state RSS, 50 windows below a comparable switcher's" is removed. It was
  also unmeasurable as written — see the numbers below.
- `ARCHITECTURE.md`'s memory rule stays, reframed as **correctness**: a leaked bitmap is a bug whatever
  the goal is. The cache bound is justified by peak footprint and summon-time fault-in latency (D9).

**What P0.7 measured before it was dropped.** A reference switcher (v11.6.0), 19 windows, macOS
26.6.2, measured with `spike/procmem` (D8's instrument applied to another pid). Two runs:

| | run 1 (pid 30462) | run 2 (pid 31918, clean process) |
|---|---|---|
| `CG raster data` virtual | 110.0 -> 114.3 MB | 23.7 MB, flat |
| `CG raster data` resident, during summons | max 18.6 MB | max 21.6 MB (91% of virtual) |
| `CG raster data` resident, idle | **0.0 MB across 35 samples** | — |
| TOTAL dirty, active -> idle | 73.3 -> 9.9 MB | max 12.5 MB |
| `Physical footprint (peak)` | 281.3 -> 305.3 MB | 36.6 -> **70.5 MB** |

Run 1's peak is **not usable**: it includes that switcher's first-run onboarding and the
permission-grant flow, which happened before sampling started. Run 2 restarted it so its peak starts
clean at 36.6 MB, and ~10 summons took it to 70.5 MB. **Treat 70.5 MB peak / ~24 MB of retained
thumbnails as the only defensible figures**, and note the two runs disagree on retained thumbnail
volume (114.3 vs 23.7 MB
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

**Weak-linking SCK needs an environment variable.** P0.6's task notes said to weak-link the framework the
way any switcher targeting an older floor does, so the binary still loads where ScreenCaptureKit is
absent — Go forces `minos 11.0` (D1) and
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
PLATFORM-LESSONS section 5 predicted this — CoreAnimation commits at the end of the runloop turn — and it
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
P0.7 hit). **The settings are prior art, not a measurement**, and they are recorded as such.
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
application is what actually enumerates switchable windows, which is why a switcher is built on AX and
uses `CGWindowList` only for identity and geometry. The roadmap had P2.3 as "AX observer registration;
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
"preserve what you have" — so a window whose title changed keeps its position. PLATFORM-LESSONS §3's rule
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

---

## D28 · P3.2: the tile layout engine, and what it deliberately does not decide — 2026-09-07

`core.Layout(n, opts, dst)` (`internal/core/layout.go`) computes the panel size and one frame per tile
from a window count and the display, in pure integer arithmetic — deterministic, and 0 allocs/op when
handed a presized `dst`. Four steps: margin `= clamp(Screen.W / 20, 32, 220)` (a fixed border is
wrong on either a 1280- or a 6016-wide display); columns `=` how many `MinTileW` tiles fit the usable
width, capped by `MaxCols` and `n`; tile width `=` usable width ÷ columns, clamped to
`[MinTileW, MaxTileW]`; then rebalance so `cols = ceil(n / rows)` and no trailing row is near-empty
(12 windows on a 1280 display → 6+6, not 10+2).

Contract extended by **adding** fields only (`api.go` was frozen and stayed so): `LayoutOpts +=
MinTileW/H, MaxTileW/H`; `Result += Cols, Rows, Overflow`. `Overflow` is a flag, never a dropped tile,
set when the wrapped panel is taller than `Screen.H` — clip or scroll is the renderer's call.

**What it does not do**, recorded so a later task does not look for it in the wrong place:

- **Selected-tile emphasis.** Not implemented (the contract marked it optional). When added it is a
  `SelectedRow int` on `LayoutOpts` with `< 0` meaning "none" — 0 is a valid tile index, so unlike
  `Rules.ActiveApp == 0` the sentinel must be negative.
- **Height does not track width.** On a narrow display `tileW` can fall well below the preferred
  `TileW` while `tileH` holds, so the tile aspect departs from `TileW:TileH`. Aspect-fitting the
  thumbnail inside that frame is the renderer's — P3.1 does exactly that (D29).

Verification cases handed to V6.6 / V6.8 (full list in `docs/tasks/P3.2.md`): the 0-alloc bench,
index alignment / row-major placement, the min/max wrap boundary, the three margin values, rebalance,
and the `MinTileW > MaxTileW` / sub-`MinTileW`-display coercions.

---

## D29 · P3.1: one flipped view draws every tile; thumbnails ride in CALayers — 2026-09-07

`panel.m` implements the frozen `panel.h`: a borderless non-activating `NSPanel` over an
`NSVisualEffectView` over one flipped `GTTileView` whose `drawRect:` paints the whole strip in a
single pass — rounded slab, per-tile background, a 2 pt selection outline, title (13 pt semibold) +
subtitle (11 pt) tail-truncated, and a framed sun/mountain **placeholder** for any tile whose image
is not captured yet (D12: that is a launch state, not a refinement). Style mask and the
`collectionBehavior` triple are `spike/panel`'s, verified there (D13).

- **Thumbnails are dumb `CALayer` sublayers**, one per tile, that never call back into Go — the
  conventional design has dozens of NSView subclasses and a C→Go hop per tile; this has zero.
  `gt_panel_tile_layer(i)` hands
  P3.3 a layer positioned over the tile's **thumbnail sub-rect** (inset 8 pt, minus a 34 pt label
  band), so an opaque `CGImage` on `contents` cannot cover the title. The split constants
  (`kTilePad`, `kLabelStrip`) live in `panel.m` and are the P3.1↔P3.2/P3.3 coordination point.
- **Show vs update is a real split** (D13: a re-order costs ~1 ms but a cold frame commits at
  ~14 ms). `gt_panel_show` resolves the screen under the mouse and orders front; `gt_panel_update`
  only re-populates and redraws. Both wrap the layer add/remove/reframe in a `CATransaction` with
  `setDisableActions:YES` — without it every keystroke animates the strip sliding for 0.25 s.
- **`gt_image_ref` is cast straight back to `CGImageRef`** to draw a warm-cache tile, relying on
  `gt_image_adopt` being a bare cast. No accessor exists in `shim.h`; the `gt_tile` comment ("the
  panel draws it") is the sanction. If `struct gt_image` ever gains a field this breaks → flag V6.
- **New link dependency `-framework QuartzCore`** in `panel.go`. The skeleton used `CALayer` already
  but was never link-tested — the gate ran `go vet` + `go test`, neither of which links a binary.
  Fixed there too (D32).

Verified by an **in-process offscreen render** (`-[NSView cacheDisplayInRect:toBitmapImageRep:]`, no
Screen Recording grant needed): styled slab, tiles, selection moving under `gt_panel_update` with no
re-show, truncated CJK/emoji titles, per-tile placeholders, a live `CGImage` on a tile layer with no
re-show, frontmost pid unchanged across create→show→update→hide. The real window-server pass —
vibrancy, window level, `collectionBehavior` over a full-screen app / across Spaces — is **V6.2/V6.3**
and needs a human.

---

## D30 · P3.3: thumbnails prefetch on one goroutine, and the prefetcher owns every handle — 2026-09-07

`thumbnail.{h,m,go}` add a `Prefetcher`: `NewPrefetcher(capacity)`, `Want([]ThumbRequest)`, `Stop()`.
`Want` (called after each show/update) publishes the visible tile set to **one** capture goroutine —
not a pool, because SCScreenshotManager serialises in the WindowServer (D12/D26) — which drives
`core.Cache` + `darwin.Capture` and sets each tile layer's `contents` through `darwin.OnMain`.

- **The memory rule, concretely:** every `ImageRef` a capture produces is held in the Prefetcher and
  `Release()`d exactly once, on `core.Cache` eviction or `Stop`. The panel never releases a tile
  image (`panel.h`). Releases are posted to the FIFO main queue, so one cannot overtake a still-queued
  `gt_thumbnail_set` for the same handle — and CoreAnimation's own retain on `layer.contents` covers
  the gap, so a tile mid-eviction does not go black. `gt_thumbnail_set` never touches `gt_image_live`.
- **A window that fails capture 3× in a row is dropped for the Prefetcher's life** (D26: 2–10% of
  captures fail because the window is gone or SCK declines, and retrying twice recovered none; a
  stale cache entry never self-heals).
- **`Stop` blocks until an in-flight `Capture` returns** — `Capture` has no cancellation and D12 saw
  it hang ~1/10 runs, so `Stop` can take up to `captureTimeout` (~2 s) then, ~57 ms normally.
  `runSwitcher` runs `Stop` in the shutdown goroutine, off any dismiss path.

Not verifiable from an agent (no Screen Recording grant on the responsible process): a thumbnail
actually reaching a tile, and `LiveImages()` settling at the cache bound after real evictions / back
to 0 after `Stop` with images held. The accounting is argued in the code; **V6.4** measures it.
`LiveImages()` was observed at 0 throughout a no-grant run — never negative.

---

## D31 · P3.4: the palette comes from the effective appearance, and needs one integrator call — 2026-09-07

`theme.{h,m,go}` read the effective appearance — the panel's
`NSVisualEffectView.effectiveAppearance` once it exists (that carries a per-window override), else
`NSApp`'s, resolved with `-bestMatchFromAppearancesWithNames:@[Aqua, DarkAqua]` so the accessibility
high-contrast appearances fold onto the right side — and push a `gt_palette` +
`NSVisualEffectMaterialHUDWindow` through P3.1's frozen setters. `AppleInterfaceStyle` from user
defaults is deliberately not used: it misses the per-app override and the auto Light/Dark schedule.

- **The watcher is KVO on `NSApp.effectiveAppearance`**, not the
  `AppleInterfaceThemeChangedNotification` distributed notification (the defaults mechanism, which
  fires only for the system-wide setting). The callback does nothing but call `goThemeChanged()` —
  the shape of P2.3b's observers. Fired exactly on real flips, never on a no-op, never after
  `StopWatchingAppearance`; add/remove and alloc/release are paired under a latch.
- **`ApplyTheme` before `gt_panel_create` cannot be replayed from `theme.*`** — there is no create
  hook and `panel.*` is frozen; the skeleton `gt_panel_set_material` early-returns while
  `g_effect == nil`. So the **integrator calls `ApplyTheme()` immediately after `CreatePanel()`**,
  which `runSwitcher` does. It is idempotent and does no IPC.
- **Vibrancy is kept on** (HUD material); the palette's `tile_bg` values are light washes that assume
  the blur behind them, and `panel_bg` is only the `GT_MATERIAL_NONE` fallback.

Light/Dark screenshots are **INCONCLUSIVE from an agent** (no window-server session) and belong to
P3.1's V6.2/V6.3 pass. The KVO path was driven by toggling `NSApp.appearance`, the same
`effectiveAppearance` signal the System Settings switch travels; the literal toggle needs a human (V6.2).

---

## D32 · Phase 3 integrated — `gotab -switch` runs the whole pipeline — 2026-09-07

The four Phase 3 tasks exposed APIs and nothing called them. `cmd/gotab -switch` is the wiring:
`CreatePanel` → `ApplyTheme` → `WatchAppearance` → `StartObservers` → the event loop on a goroutine →
**the AppKit run loop on the main thread**. A `panelRenderer` on `Loop.OnState` turns
model + order + selection into `core.Layout` frames and `[]darwin.Tile`, calls `ShowPanel` on the
first visible state and `UpdatePanel` after, `HidePanel` when hidden, and feeds the `Prefetcher` the
visible set — every AppKit call marshalled through `darwin.OnMain`.

- **`runloop.{h,m,go}`: `RunLoop()` / `StopRunLoop()`.** `RunLoop` is `CFRunLoopRun()` after
  `-[NSApp finishLaunching]`, **not** `-[NSApp run]` — the panel is ordered in with
  `orderFrontRegardless` and never becomes key, so nothing depends on NSApp's event-dispatch state,
  and `CFRunLoopStop` from the shutdown goroutine breaks it cleanly where `-[NSApp run]` would wait
  for the next event. libdispatch's main-queue source, the NSWorkspace notification source and the CA
  transaction observer are all on that one loop.
- **This closes P2.3b's open half.** With the main run loop turning, `NSWorkspace`'s launch/quit
  notifications are delivered, so `-switch` sees applications that start after it. `-watch` still has
  no run loop and keeps its backstop ticker.
- **`Loop.Activate` now raises.** P2.5 landed, so `internal/app/loop.go`'s `Activate` calls
  `darwin.Raise(sel.ID)` then hides, in that order. The `-switch` demo never posts `Activate` (it
  would reorder the user's windows), so this path is exercised only once a hotkey exists → **V6.5**.
- **No hotkey.** P0.2's `CGEventTap` is a spike and nothing routes ⌥⇥ into `Loop.Post`. `-switch`
  scripts one `Summon` + a slow `Cycle` tick so the pipeline is demonstrable. Wiring the hotkey is
  its own task — **P3.5**, un-roadmapped until now the way `internal/app` was before P2.7.
- **`panelRenderer.onState` allocates two slices per state change** (`[]Tile`, `[]ThumbRequest`):
  they go to an async `OnMain` closure and to `Prefetcher.Want`, so a reused backing array would be
  overwritten by the next state before the main thread reads it. `core.Layout` stays 0-alloc; this
  bridge does not → **assumption, V6.8**.
- **The gate gained `go build ./...`.** `go vet` and `go test` compile without linking a binary, so a
  `#cgo LDFLAGS` line missing a `-framework` slips both — P3.3 and P3.4 each hit this with QuartzCore.
  It is not the universal `.app` build (still V6.7); it just makes "it links" a gate condition.
- **Pre-existing, noted for V6.7:** `capture.m` uses `SCScreenshotManager` (macOS 14+) against a
  12.0 deployment target, so `scripts/build.sh` prints `-Wunguarded-availability-new` warnings. The
  runtime-absent path is already handled (`ErrUnavailable`, D26); the fix is an `@available` guard.

Smoke-tested with no grants: `gotab -switch` builds the panel, runs the loop, scripts the summon,
degrades cleanly (`OnError` prints the missing grant, empty panel), and exits 0 on SIGINT with the
shutdown draining the prefetcher and observers before it stops the run loop. Pixels on screen are
V6.2/V6.3.

---

## D33 · P3.5: the ⌥⇥ tap runs on its own thread and owns the gesture state — 2026-09-07

`hotkey.{h,m,go}` install a session `CGEventTap` at `kCGHeadInsertEventTap` — the only position from
which the switcher sees ⌥+Tab before the focused app and can swallow it — and post `Summon` / `Cycle`
/ `Activate` / `Dismiss` into `Loop.Post`. `spike/hotkey` (P0.2/D15) is the prior art: the head
insert, the `kCGEventTapDisabledByTimeout` re-enable (the system disables a slow tap exactly once,
by that event; silence there is a dead hotkey), and reading `CGEventGetTimestamp` for the latency
V6.1 will measure.

- **Own thread, like `observe.m`.** `gt_hotkey_start` detaches a `gotab.hotkey` thread that owns the
  tap, its run-loop source and a `CFRunLoopRun()` until `gt_hotkey_stop` sets `g_stopping` and calls
  `CFRunLoopStop`. Adding the tap source to the main run loop `runloop.m` already runs was the
  simpler option and was rejected: a keystroke is the most latency-sensitive thing in the app and
  must not wait behind a `drawRect:` or a CA commit. Teardown is `observe.m`'s idiom — clear the
  atomic loop pointer, stop it, wait on a semaphore.
- **The gesture state machine lives on the tap thread**, not in Go. `g_armed` is set by the first
  ⌥+Tab of a hold and cleared on commit/dismiss; the first Tab emits `SUMMON_*`, later ones
  `CYCLE_*`, and Option-release emits `ACTIVATE` only if armed. So `cmd/gotab`'s `postGesture` is a
  stateless `switch` — `SUMMON_FWD` becomes `Summon` + `Cycle(Forward)`, so a single ⌥⇥ tap lands on
  the *next* window (the expected switcher behaviour).
- **What is swallowed:** ⌥+Tab keydown and — while armed — its keyup (so the app underneath gets no
  orphan keyup), and Esc while armed. A `kCGEventFlagsChanged` is never swallowed (other apps track
  Option state). Autorepeat on a held Tab (`kCGKeyboardEventAutorepeat`) is dropped — ~15×/s is too
  fast to cycle on, and a user who wants to spin can tap.
- **`Loop.Activate` raises** (wired in D32): `postGesture` → `Activate` → `darwin.Raise(sel.ID)` →
  hide. Releasing Option is now a real switch.
- **No grant → no tap.** `StartHotkey` returns `ErrNotTrusted` (an ungranted tap installs and never
  fires — indistinguishable from broken), and `gotab -switch` falls back to `demoDriver`. That
  fallback path is smoke-tested: clean SIGINT exit, `StopHotkey` safe when never started.

**assumption → V6.1 / V6.5:** the callback latency (event → `goHotkeyGesture`) against the < 5 ms
budget, and the first C→Go crossing on the tap thread (the runtime meeting a thread it has never
seen), are unmeasured — a synthetic event's timestamp is not on the HID path, so P0.2's
`spike/hotkey -manual -n 20` needs a human. The granted round trip — a real ⌥⇥ seen, swallowed,
summon→cycle→raise — is V6.5.

---

## D34 · P4.1: settings live in the bundle-id CFPreferences domain, keyed by identity not a string — 2026-09-07

`internal/prefs` is the pure-Go schema: a flat `Prefs` struct, `Default()`, `Load(Reader)`,
`Save(Writer)`, `Set("Key=Value")` for the CLI, and the one-way derivations `LayoutOpts()` / `Rules()`.
Keys are the exported field names, so a missing or wrong-typed key falls back to `Default()` — never
Go's zero — and `gotab -prefs MaxColumns=5` and `defaults write app.gotab MaxColumns 5` address the
same key. `internal/platform/darwin.Prefs` implements `Reader`/`Writer` over
`CFPreferencesCopyAppValue` / `CFPreferencesSetAppValue` on `kCFPreferencesCurrentApplication`
(`prefs.{h,m,go}`).

- **The domain is the running binary's identity, not a literal `"app.gotab"`.** From
  `build/GoTab.app` it is `CFBundleIdentifier` (`app.gotab`) and `defaults read app.gotab` shows the
  schema — verified: `MaxColumns = 3`, `HotkeyModifiers = 524288`, `BlockedApps = ()` after
  `gotab -prefs MaxColumns=3`. Under `go run` / a bare `go build` binary it is that binary's own name
  (`~/Library/Preferences/<name>.plist`), so a dev build writes a throwaway domain and cannot corrupt
  the real prefs. This is the same responsible-process identity rule TCC uses (PLATFORM-LESSONS §5), and
  it is the honest behaviour, not a limitation to fix.
- **`Save` writes the whole record, not a diff.** A field at its default is still persisted, so a
  later change to `Default()` cannot silently move a user's setting.
- **String arrays cross cgo `'\n'`-joined** (`BlockedApps`). A window-server application name never
  contains a newline; the join is lossy only for a value that cannot occur.
- **The `Appearance` override is an `atomic.Int32` in `theme.go`** that `ApplyTheme` consults; a
  forced Light/Dark makes the `WatchAppearance` callback's re-apply a no-op. No `theme.m` change.

**Two schema fields have no consumer yet, and this is deliberate — the schema is defined once:**

- The **filter** fields (`ShowMinimized`, `ShowHidden`, `ShowOtherSpace`, `ActiveAppOnly`,
  `BlockedApps`). `core.Filter` (P1.3) and `Prefs.Rules()` exist, but `internal/app`'s `doRescan`
  orders **every** window — the loop has never filtered. Wiring it (a `Loop.Rules` field, and a
  `core.Order` that rebuilds from a filtered row set) is its own change, and P4.4 touches the same
  code for Spaces. `assumption` → these prefs are inert until then.
- The **hotkey chord** (`HotkeyKeyCode` / `HotkeyModifiers`, defaulting to 48 / `0x80000` = ⌥Tab).
  `hotkey.m`'s tap matches a fixed keycode; making `gt_hotkey_start` take a chord is small but
  rebinding is a settings-UI feature, so it is P4.2's. `assumption` → not yet rebindable.

`assumption` → **V6.8**: `panelRenderer` still hard-codes `Scale: 2` after `LayoutOpts()`. A task that
reads the display's real backing scale (P4.4) removes it. *(Done in P4.4 / D37, below.)*

---

## D35 · P4.2: one settings window, one assign form, and a rebindable chord — 2026-09-07

`internal/platform/darwin/settings.{h,m,go}` build a fixed-size `NSWindow` with a control per wired
setting — 4 filter checkboxes, a blocked-apps text field, an Appearance popup, Columns and
Thumbnail-cache steppers, and a hotkey recorder — laid out with manual top-down frames (the window
does not resize, so a stack view earns nothing). `gotab -settings` opens it on its own run loop.

- **One assign form.** Every control funnels through `emit(key, value)` →
  `goSettingsAssign("Key=Value")` → `prefs.Set` → `Save`. Reusing P4.1's `Set` parser means no
  per-control marshalling and no second schema, and the CLI (`-prefs Key=Value`), `defaults(1)`, and
  the window all drive the same path — so the `Key=Value` round trip is verified via `-prefs` even
  though the window's pixels are not reachable from here.
- **`darwin` still does not import `internal/prefs`.** `OpenSettings` takes a primitives-only
  `SettingsValues`; `cmd/gotab` maps a `prefs.Prefs` onto it. `darwin.Prefs` has always implemented
  `prefs.Reader`/`Writer` structurally, and this keeps that boundary.
- **The hotkey chord is configurable end to end.** `gt_hotkey_start` now takes `(keycode,
  modifiers)`; `g_chord_key` / `g_chord_mods` replace the hardcoded Tab + Alternate, and the
  "modifiers released → commit" edge became `g_chord_was_held` transitioning, which works for any
  modifier set. `modifiers == 0` is rejected in three places — `prefs.Set`, `StartHotkey`, and the
  recorder (`NSBeep`) — because a bare-key chord would be swallowed for every application. If a chord
  includes Shift, backward-cycle is unavailable (Shift cannot mean two things); an acceptable corner.
- **`HotkeyDisplay` (`hotkey.go`) is the only chord formatter** — a small keycode table plus the four
  modifier symbols. The recorder emits raw values and Go formats the label, so the string is produced
  in one place; an uncommon keycode shows as its number.
- **`-settings` is its own process** and the window says nothing misleading about it: it writes the
  plist, and a running `gotab -switch` re-reads settings only on its next launch. The app switches to
  `NSApplicationActivationPolicyRegular` while the window is open (it is `Accessory` for the panel);
  `-settings` never creates the panel, so there is no conflict.

**Still not wired:** the filter checkboxes **write** `ShowMinimized` etc., but `internal/app`'s loop
still does not **read** `core.Rules` — P4.1's open item, and P4.4's to close (Spaces touch the same
code). `TileWidth`/`TileHeight` stay CLI-only (`0 = auto` reads badly as a stepper).

---

## D36 · P4.3: onboarding is an alert plus a poll, and it must survive being run headless — 2026-09-07

`internal/platform/darwin/permissions.{h,m,go}` handle a missing grant: a modal `NSAlert` names it,
says what it is for, and on "Open System Settings" opens the relevant Privacy pane — and, for Screen
Recording, calls `CGRequestScreenCaptureAccess` so the app gets a row in that list. `gotab
-permissions` and `gotab -switch` both call it and then **poll** `CheckPermissions` every 750 ms
until the grant appears.

- **Recovery is a poll, not an observer.** TCC exposes no "grant changed" signal worth building on; a
  poll on `gt_trusted()` / `gt_can_record()` picks a Settings toggle up within ~1 s. `-switch` gates
  on Accessibility (without it nothing below works), caps the wait at 5 minutes, then quits with a
  re-open hint; Screen Recording missing is a one-line warning and it runs on. macOS relaunching
  gotab itself on the Accessibility grant is not a "relaunch loop" — the fresh process passes the
  gate and never prompts.
- **`[NSAlert runModal]` cannot be interrupted.** Run with no window server it blocks forever and
  ignores SIGINT — a first cut hung under `kill -INT` and needed `kill -9`. So `gt_permissions_prompt`
  checks `[[NSScreen screens] count] == 0` first and returns `GT_ERR_INTERNAL`; `PromptPermissions`
  reports that as `shown == false` and the caller prints the deep links instead.
- **Modal from Finder, text from a shell.** `stderrIsTTY()` decides. A `.app` from Finder has no
  controlling terminal, so the alert is the only channel; from a shell an alert stealing focus for
  text the user could read inline is worse, so the TTY path prints the `x-apple.systempreferences:`
  links and opens the pane directly. Verified via a pty: prints, opens the pane, enters the poll, ^C
  exits cleanly.
- **No new window.** An `NSAlert` needs no controller, no layout, no in-window timer. The onboarding
  "UI" is the alert plus the System Settings pane — the native pattern, and the least code.

**V6.9** is the human half: the modal itself, and a full revoke → start `-switch` → grant in Settings
→ switcher comes up without relaunching. The poll and the headless fallback are exercised here.

---

## D37 · P4.4: the filter goes live, and the panel is sized for the display it lands on — 2026-09-07

Two things the switcher had the parts for since Phase 2/3 but never connected.

**The filter.** `internal/app`'s `doRescan` has ordered *every* window since P2.7. Now `Loop.Rules`
(set from `Prefs.Rules()` — the P4.2 checkboxes) runs the model through `core.Filter` (P1.3) →
`core.Order.RebuildFrom` on every rescan, so a window that fails the rules is not in `Order` and the
renderer never draws it and cycling skips it.

- **Rebuild every pass, not "only when membership changed".** The old gate missed a window
  minimizing or the current Space flipping — filter *inputs* that move without the window set
  changing. It is safe to rebuild always: `RebuildFrom` sorts by `FocusSeq`, which a title or frame
  change never touches (`doRescan` upserts a zero `FocusSeq` = "preserve", D22), so a pass where
  nothing filter-relevant moved yields byte-identical `Order.Rows` — `-watch`'s print-on-change stays
  quiet, which is P2.7's MRU-stability check. `Rebuild` and `RebuildFrom` now share `sortByFocus`.
- **`CurrentSpace` is runtime, not a setting.** `Prefs.Rules()` leaves it 0; `doRescan` fills it from
  `darwin.CurrentSpace()` each pass. A Space switch doesn't always raise an AX notification, but it
  changes on-screen state, which does, so a rescan follows it.
- **`Enumerate` fills `core.Window.Space`** in one extra crossing (`SpacesOf`, P2.4: 1.4–4.8 ms for
  ~60 windows — off the summon path, D5). A SkyLight failure leaves every Space 0, which
  `core.Rules.Allows` reads as "don't filter by Space" (`TestFilterUnknownSpace`).
- **`-watch` opts out** — it sets `ShowMinimized/Hidden/OtherSpace` true so it stays a raw view of
  the enumeration; `-switch` is the filtered one.

**The display.** `core.Layout`'s margin scales with `Screen.W` and its `Overflow` needs `Screen.H`,
and thumbnails want the real backing scale. `panelRenderer.onState` now sets `LayoutOpts.Screen` and
`Scale` from `darwin.ActiveScreen()` — the screen under the mouse, resolved by the same
`screen_under_mouse()` helper `gt_panel_show` uses for placement, so sizing and placement agree.
This retires P4.1's hard-coded `Scale: 2`. `ActiveScreen` reads `NSScreen` off the loop goroutine;
AppKit documents that as main-thread-only but the values are immutable snapshots and off-main reads
are common — an `assumption` in `panel.go`, V6.2 / V6.5.

**Still inert:** `ActiveAppOnly` — `Prefs.Rules()` sets the bool but not `Rules.ActiveApp`, so
`Allows` skips it; it needs the frontmost pid captured on `Summon` (a `darwin.FrontmostApp()` and a
loop field), a small follow-up. And the panel *itself* across Spaces is P3.1's `collectionBehavior`,
still → **V6.2**.

---

## D38 · P4.5: the bundle packages and round-trips; the rest is a machine — 2026-09-07

`build.sh` / `install.sh` / `uninstall.sh` already existed; P4.5 finished them.

- **The bundle is signed, not just the binary.** `codesign --force --sign - "$OUT"` on the `.app`
  seals `Info.plist` and `_CodeSignature/CodeResources`; `codesign --verify --strict` right after
  proves it took, and `install.sh` re-verifies the installed copy — a copy that lost its signature
  (a bad transfer, a filesystem that drops xattrs) is caught before the first double-click, not at it.
- **`spctl -a` says `rejected`, and that is correct.** Gatekeeper's assessment only gates a
  *quarantined* copy — downloaded, AirDropped. A locally built, locally installed app has no
  `com.apple.quarantine` xattr and launches. A copy carried to another machine, or a notarized
  release, is V6.7; `build.sh` and `install.sh` both say a public release needs a Developer ID.
- **No-flags default is launch-context-sensitive.** `os.Executable()` ending in
  `.app/Contents/MacOS/` means Finder started it and the switcher is what is wanted — a usage string
  to a stderr nobody reads would be an app that does nothing. From a shell, the help text stays.
- **Two version strings.** `git describe` on a tag-less repo is a bare hash, and
  `CFBundleShortVersionString` is meant to be dotted numbers, so `SHORT_VERSION="0.1.0"` is the
  marketing string and `git describe` is `CFBundleVersion` and `-X main.version` — the build id a bug
  report can name.
- **`--purge` clears TCC too.** `tccutil reset Accessibility app.gotab` and `tccutil reset
  ScreenCapture app.gotab` (that service name, not `ScreenRecording`) — idempotent, so it is
  best-effort and quiet when the grant was never given. Verified against a temp `GOTAB_APPS` dir: app,
  plist, and both TCC rows gone, `tccutil` reported success.

**V6.7** is now purely the machine half: a first launch from `/Applications` on a genuinely clean
account, and on a real macOS 12 host (the `minos`/plist check proves they *agree*, not that a 12.0
binary *runs* — D17). No icon yet (`CFBundleIconFile` absent); that needs artwork and is Phase 5.

---

## D39 · P5.3: the updater is a plain version check, not Sparkle — 2026-09-07

The roadmap left P5.3 as "decide: Sparkle via cgo, or plain download". **Decided: a plain check.**
`internal/update` is pure Go, standard library only — `Check(ctx, current)` does an HTTPS GET of a
small JSON manifest, compares versions, and returns whether a newer build exists and its URL. It does
not download, verify, or relaunch anything. `gotab -check-update` is the entry point; a launch-time
check is a later, optional addition.

Why not Sparkle:

- **The bundle is ad-hoc signed (D38).** Sparkle's value is in-place download-and-relaunch, and that
  story only holds with a Developer ID signature + notarization — a `spctl`-rejected ad-hoc app that
  replaces itself is not something to ship. Until notarization exists (V6.7's machine half), Sparkle
  would be infrastructure with nothing to stand on.
- **Clean break.** The project inherits nothing from any existing switcher — no Keychain, no
  preference migration, no Sparkle continuity (D6, ARCHITECTURE.md). Mature Swift switchers vendor
  Sparkle, a crash reporter and a shortcut recorder (PLATFORM-LESSONS.md); GoTab vendors nothing and
  this keeps it that way.
- **cgo cost and surface.** Sparkle is an Objective-C framework — another weak link, another
  `SUPublicEDKey` in the plist, an EdDSA signing step in `build.sh`, an appcast host. A `net/http`
  GET and a 30-line semver compare is the whole of the alternative.

**assumption → V6.11:** `FeedURL` (`https://gotab.app/appcast/latest.json`) has no host and the check
has only run against a local file. Downloading + verifying + relaunching a new build is explicitly
out of scope — it is a separate task gated on notarization, not a V6 row.

---

## D40 · P5.1: the i18n scaffold — one .lproj dir, two file formats, and a locale punt — 2026-09-07

`internal/i18n` is pure Go (embedded `en.json` as the single source of truth for the key set; a
non-English locale is a partial overlay merged over it, swapped through an `atomic.Pointer` so `T`
reads lock-free after a startup `SetLocale`/`Load`).

Three shape decisions worth recording:

- **Two file formats per locale, by necessity.** GoTab's user-facing strings live in two runtimes.
  Go code calls `i18n.T` and reads `<locale>.lproj/gotab.json`. Objective-C in
  `internal/platform/darwin` calls `NSLocalizedString`, which AppKit resolves from
  `<locale>.lproj/Localizable.strings` in the bundle — that path is not ours to change. So each
  locale is one `.lproj` directory holding both files, kept in step by hand. A single format would
  mean either teaching Go to parse `.strings` or stopping AppKit from doing the lookup it does for
  free; both cost more than mirroring a dozen keys.
- **Locale selection is `GOTAB_LOCALE` → `LANG`, for now.** The correct source is
  `CFLocaleCopyPreferredLanguages` / `AppleLanguages`, which is cgo — and `internal/i18n` is
  deliberately cgo-free so it stays in the pure layer with `core` and `prefs`. A
  `darwin.PreferredLanguages()` feeding `cmd/gotab`'s `initLocale()` is the follow-up; the env vars
  cover development and the `de` proof in the meantime.
- **Scaffold, not a sweep.** Only the permissions onboarding strings (the NSAlert and the CLI
  prompt/wait lines) are routed through `i18n` in this pass, with a `de` overlay that exists to
  exercise the loader rather than to ship German. The CLI usage text, the settings window and the
  panel are a later migration — doing them now would have collided with P5.2 (`settings.m`,
  `panel.m`) and bought nothing the scaffold does not already prove.

---

## D41 · P5.2: the panel speaks to VoiceOver from one container, and press stays on the old path — 2026-09-07

P5.2 asked for a screen-reader-usable switcher. `panel.m` draws every tile into one `GTTileView`, so
there is no per-tile `NSView` for AppKit to expose. The shape that fell out:

- **The drawing view is the accessibility container.** `GTTileView` answers `isAccessibilityElement`
  NO, `accessibilityRole` group, `accessibilityLabel` "Window switcher", and hands back one
  synthetic child per tile from a file-static array kept in step with `g_tiles` by `a11y_sync()` —
  the same grow/shrink-at-the-tail discipline as `g_tile_layers`.
- **`NSAccessibilityElement`, for the flipped-frame conversion.** Each child is a
  `GTTileElement : NSAccessibilityElement` (button role, `"<title>, <app>"` label, `selected` bit).
  `accessibilityFrameInParentSpace` is set to the tile's own top-left `x/y/w/h` — the class does the
  flipped-parent math, so what is spoken and what is drawn cannot drift.
- **The announcement is debounced and re-armed.** `panel_populate` (shared by show and update) posts
  `NSAccessibilityAnnouncementRequestedNotification` at High priority plus a selection notification
  when the selected index moves, and `NSAccessibilityLayoutChangedNotification` when the count
  changes. `g_a11y_last_selected` / `g_a11y_last_n` keep a no-op redraw (a thumbnail arriving, a
  palette flip) silent; `gt_panel_hide` resets them to `-1` so every summon speaks its first
  selection even when it matches the last one.
- **Press is a deliberate no-op.** `panel.h` is frozen and exposes no callback to raise a window
  from `panel.m`. `GTTileElement.accessibilityPerformPress` returns NO; a VoiceOver user activates
  the way a sighted user does — release the modifier, and the hotkey tap drives `Loop.Activate` →
  `darwin.Raise`. Wiring press-to-raise would need a new exported function; it is a follow-up, not an
  escalation, because the cycle-and-release flow is already usable without sight.
- **Settings: labels only where a control renders no text.** `setAccessibilityLabel:` on the two
  steppers, the Appearance popup, the blocked-apps field and the hotkey recorder. The filter
  checkboxes already speak their titles and were left alone.

**assumption → V6.10:** none of this has been *heard*. An agent host has no screen reader — the same
wall P3.1's screenshot (V6.2/V6.3) and V6.1's keypress hit. A human runs VoiceOver against
`gotab -switch` and `gotab -settings` and confirms each tile is announced with title + app, the
selection is spoken on every ⌥⇥ cycle, and every settings control has a spoken label.

---

## D42 · CI/CD: releases are cut by a tag, and the update feed is GitHub Pages — 2026-09-08

P5.3 left `FeedURL` pointing at `https://gotab.app/appcast/latest.json`, a host that never existed
(D39), and there was no release process at all. Both are now GitHub infrastructure, so nothing new
has to be paid for or operated.

- **A `v*` tag is the entire trigger.** `git tag -a v0.2.0 -m "…" && git push origin v0.2.0` starts
  `.github/workflows/release.yml`: run the gate, `build.sh`, `ditto`-zip the `.app`, `gh release
  create` with the zip + its SHA-256, render `latest.json`, deploy Pages. No "create the release in
  the UI first" step — the tag is the input and the commit it points at is the provenance.
- **One build path, not two.** The workflow calls the same `scripts/build.sh` a developer runs;
  there is no CI-only build. Its one concession is `SHORT_VERSION="${GOTAB_SHORT_VERSION:-0.1.0}"`,
  so the tag drives `CFBundleShortVersionString` while `git describe --tags` (now reachable —
  `fetch-depth: 0` on checkout) drives `CFBundleVersion` and `-X main.version`. `parseVersion`
  already strips the leading `v`.
- **The manifest is rendered from the checked-in template.** `resources/appcast/latest.json` stops
  being a stale example and becomes the file the workflow reads: `jq` overrides `version` / `url` /
  `notes` (the last from the annotated tag's subject line) and leaves `min_macos` — the single
  source of truth for the manifest's OS floor stays in the repo, next to `build.sh`'s `MIN_MACOS`.
- **Pages via `actions/deploy-pages`, no `gh-pages` branch.** Source = "GitHub Actions"; the
  `release` job uploads a `_site/` artifact (the manifest plus a one-line `index.html` so the root
  isn't a 404) and a separate `deploy-pages` job publishes it. Separate on purpose: a first tag
  pushed before the repo is public — or before the Pages source is switched — still produces a
  usable Release, and only the Pages step needs re-running.
- **`FeedURL` → `https://moustafa-elgammal.github.io/gotab/latest.json`.** Inert until the repo is
  public and Pages is enabled, which is the same "placeholder until a host exists" status D39
  recorded — only now the host is concrete and V6.11 has something real to run against.
- **First-party actions only.** `actions/checkout`, `actions/setup-go`, `actions/upload-pages-artifact`,
  `actions/deploy-pages`, and the preinstalled `gh` CLI. No `softprops/action-gh-release`, no
  `peaceiris/actions-gh-pages` — the project vendors nothing (D39, ARCHITECTURE.md) and that applies
  to the pipeline too.

**Still out of scope, and still gated on an Apple Developer account:** notarization / Developer ID
signing, and in-app download + verify + relaunch. The attached zip is ad-hoc signed (D38), so a
downloader on another machine needs a right-click → Open. A notarization step in the workflow is the
fix and is a separate future task, not a V6 row. `BUNDLE_ID` stays `app.gotab` — renaming it now
orphans the CFPreferences domain and the TCC grants (D34).

**assumption → V6.12 / V6.11:** the pipeline has not run — the repo is private, Pages is off, and
there are no tags. V6.12 is the row for "it runs once, end to end"; V6.11 then becomes runnable.

---

## D43 · the app icon: a portrait source, a padded square master, `.icns` built by `sips` — 2026-09-08

The art was delivered as `resources/assets/ico.png` — the GoTab gopher, 496×664, portrait, with its
own white sticker outline and a transparent field. macOS icons are square (16…1024). Cropping the
gopher or squashing it to fit were both rejected; instead it is centred on a 1024×1024 transparent
canvas at 92% of the tile, and *that* — `resources/assets/icon-1024.png` — is the master the build
consumes. The portrait `ico.png` stays in the tree as the human-editable original.

- **Derived, not vendored.** `scripts/build.sh` renders ten exact-size PNGs with `sips` and packs
  them into `Contents/Resources/AppIcon.icns` with `iconutil`, at build time. Same shape as the
  rendered `latest.json` (D42): one committed source, the packaged form is a build artifact, never
  committed. `sips` and `iconutil` are base-system — the same dependency tier as the script's
  existing `lipo` / `otool` / `plutil` / `codesign`, so no new toolchain.
- **The square master *is* committed** rather than derived at build. Padding a portrait image onto a
  transparent square needs a real compositor, not `sips`; doing it once (a short Swift/CoreGraphics
  snippet, kept in `resources/assets/README.md`) keeps every build on `sips` + `iconutil` alone.
- **`CFBundleIconFile` is `AppIcon`**, extensionless by convention. A missing master is not fatal:
  `build.sh` warns and skips, the same stance as an absent locale tree (D40) — the app still
  launches, just with the generic bundle icon.
- **16 and 32 px are weak.** The source is a detailed illustration; at list-icon sizes the
  window-switcher badge on the gopher's belly turns to mud. A simplified small-size glyph is a
  separate hand-drawn asset and is not done here.
- The app is `LSUIElement` (D34, P4.2) so there is no Dock tile, but Finder, the Login Items list,
  and the TCC / permissions prompts all render the bundle icon — it earns its place.

---

## D44 · the `panelRenderer.onState` per-state-change allocation is accepted, not removed — 2026-09-08

V6.8's job was to re-check the 0-alloc hot path now that Phases 2–5 wire real data through it, and to
settle the `assumption` the roadmap tagged at Phase 3: `cmd/gotab`'s `panelRenderer.onState` builds
two fresh slices — `make([]darwin.Tile, n)` and `make([]darwin.ThumbRequest, n)` — on every state
change.

- **The `core` hot path is still clean.** Every `internal/core` benchmark reports `0 allocs/op`, and
  a new composite `internal/app` benchmark (`BenchmarkHandleGesture`: `Summon` → `Cycle` → `Cycle` →
  `Dismiss` against 20 real windows) is `19.8 ns/op, 0 B/op, 0 allocs/op`. Filtering and rebuilding
  the order every rescan (P4.4 / D37) did not cost the hot path an allocation.
- **The bridge allocation is measured: 2 allocs, `n × 112` bytes — 784 B at 7 windows, 2240 B at
  20.** (`unsafe.Sizeof`: `Tile` 80 B, `ThumbRequest` 32 B.) Once per gesture — a summon, or one
  cycle keystroke — not once per frame and not per prefetched thumbnail.
- **Accepted.** The slices are fresh each call for a real reason: they are handed to an async
  `darwin.OnMain` closure and to `pf.Want`, and the loop goroutine would otherwise overwrite a
  reused backing array before the main thread read it. Removing the allocation needs a `sync.Pool`
  (closure returns the buffer) or an N-deep ring — lifecycle complexity that does not pay for itself
  against 2 KB on a keystroke that already crosses into AppKit and sits behind the ~46 ms capture and
  ~1.3 ms draw the summon budget is actually spent on (D12, D13). ARCHITECTURE.md's 0-alloc rule is
  about `core`'s kernels; this is the bridge above them.
- **The escape hatch, on record:** if a CPU profile of a real session ever shows this mattering, lift
  the tile/request build into a `panelRenderer` method with a pooled buffer. Not done now because
  nothing measures it as a problem.

---

## D45 · V6.1 — the hotkey callback lands in 3.2 ms worst case, inside the 5 ms budget — 2026-09-08

`spike/hotkey -manual -n 20 -timeout 120000`, 20 real ⌥⇥ presses by a human, Accessibility granted
to the running terminal. This is the Phase 0 debt P0.2 left as code without a number (D15) — the last
of Phase 0's four targets to get a measurement.

| metric | mean | p50 | worst | budget |
|---|---|---|---|---|
| event → callback | 0.60 ms | 0.28 ms | **3.20 ms** | **< 5 ms** |
| C → Go → C round trip | 33.6 µs | 34.7 µs | 48.9 µs | — |
| first-ever crossing | 28.0 µs | — | — | — |

- **PASS at 64 % of budget.** Worst case 3.199 ms (press 18 of 20); one other blip at 2.533 ms
  (press 16). Every other press was under 0.7 ms. The tap is session-level, inserted at the head.
- **The first crossing is not an outlier.** 28.0 µs — squarely in the steady-state band, not the
  cold-thread penalty P0.2 anticipated for a callback arriving on a CoreFoundation thread the Go
  runtime has never seen. The instrument's own two-clock-read baseline is 0.009 µs, so the crossing
  numbers are real.
- All 20 presses swallowed (40 events seen = keydown + keyup); `kCGEventTapDisabledByTimeout` never
  fired, so the run is a clean measurement rather than one recovered from a stall.
- The P0.2 and P3.5 `assumption` tags ("the hotkey arrives inside 5 ms") are discharged. P0.2's box
  moves to `[x]`; Phase 0's target table now has a number behind all four rows.

---

## D46 · V6.5 — the switcher shows windows it cannot raise; Phase 7 is the fix — 2026-09-08

Driving the assembled `gotab -switch` on a real machine (the first thing the on-machine session did
after V6.1). Enumeration is clean; **raise is not**.

**What passed.** `gotab -list` on a live desktop: the CoreGraphics ↔ Accessibility join (P2.3c / D21)
is correct — every titled layer-0 window is either switchable or excluded with a named reason, no
titled window wrongly dropped, and the `cg`-only recovery visibly fires (windows AX does not
enumerate are pulled in from CoreGraphics). Raising a window that Accessibility *can* see
(`OriginBoth` / `OriginAXOnly`) works — 3 of 3 in a direct test (`spike/raisetest`): the window comes
forward and its app becomes frontmost.

**What failed.** Every raise of a `cg`-only window (`OriginCGOnly`) returns `GT_ERR_NO_WINDOW`
(`ErrNoWindow`). In one `-switch` session, 12 of 12 selections failed this way — the user was
cycling onto the two windows AX could not see (Activity Monitor and Claude, both on another Space at
the time) plus one Chrome tab whose window id had already churned.

**Root cause.** `gt_window_raise` → `copy_window_element` resolves a `CGWindowID` by asking the
owning process for `kAXWindowsAttribute` and matching on `_AXUIElementGetWindow`. That attribute
lists only the windows on the current Space, so a window the join recovered *because* AX could not
see it cannot be resolved here either — same blind spot D20 found for enumeration, now inherited by
the action path. The fallback scan of every regular application hits the same wall.

**The gap is structural, not a bug in one function.** P2.3c exists to show windows AX cannot see;
P2.5 can only act on windows AX *can* see. So the switcher renders tiles that do nothing. Stale ids
(Chrome's New-Tab window recycling) and ⌘W'd windows of still-running apps can linger the same way.

**Decision: a new Phase 7 — Actionability.** P7.1: when a window will not resolve but its owning
process is alive, activate the *application* and return success — the user reaches what they were
aiming at even if the specific window is not ordered. P7.2: prune a window once its pid is gone or it
has fallen out of enumeration. P7.3 (optional, private-API bet): a real Space-switch-then-raise so
P7.1's app-only fallback is the exception. Default (P7.4): keep showing an app-reachable window
rather than hide it. Verification lands as V6.13 / V6.14 in Phase 6's table, per the deferred-testing
rule (a new phase gets rows in that table, not its own tests — AGENTS.md).

**Scratch instrument:** `spike/raisetest/` — enumerate, then `darwin.Raise` every window and print
the result per origin. Kept through Phase 7; delete when V6.13 closes.

---

## D47 · Phase 7 — an un-raiseable tile still reaches its app; dead windows leave — 2026-09-08

D46 found the switcher rendering tiles it could not action. Phase 7 closes that gap from both ends.
**Code only — on-machine verification is V6.13 / V6.14, and V6.2 for P7.3.** P7.2 and P7.3 were built
in parallel worktrees against disjoint file sets (`internal/app/loop.go` + new `process.go` vs
`space.{h,m}` + `action.m`) and merged `--no-ff`; the combined gate is green with no cgo warnings.

**P7.1 — raise falls back to the application.** When `copy_window_element` returns no element with
`GT_ERR_NO_WINDOW` and the owning pid is a running application
(`runningApplicationWithProcessIdentifier:` non-nil), `gt_window_raise` calls `activate_app(pid)` and
returns `GT_OK`. The specific window is not ordered; the app comes forward, which `Loop.Activate`
already treats as a switch. `GT_ERR_TIMEOUT` / `GT_ERR_NOT_TRUSTED` are not rerouted — a wedged app
or a revoked grant is a different answer. A dead owner stays `ErrNoWindow`.

**P7.2 — dead windows leave the model.** `doRescan`'s prune already dropped a window that fell out of
both enumerations for a pass (the ordinary ⌘W close — confirmed against `Enumerate` in `window.go`,
now commented). It now also drops a window still being enumerated whose owning pid has exited — a
`cg`-only window the join recovered can outlive its app by a rescan or two on a stale CoreGraphics
entry. `darwin.ProcessAlive(pid)` is pure Go (`syscall.Kill(pid, 0)`: `nil` / `EPERM` alive, `ESRCH`
gone) — POSIX, not CoreGraphics, so no cgo crossing and not behind the frozen shim. One `Kill` per
distinct pid per rescan, memoized in a per-pass map preallocated in `New` like `present` / `allowed`;
no allocation added to the path, and the backwards swap-with-last prune keeps its invariant.

**P7.3 (optional, private-API bet) — Space-aware raise.** Before P7.1's fallback, `gt_window_raise`
calls `gt_space_switch_to_window(wid)`: if the window is on another Space, SkyLight
(`CGSManagedDisplaySetCurrentSpace`, display from `CGSCopyManagedDisplayForSpace`, `CGSShowSpaces` /
`CGSHideSpaces` best-effort) makes that Space current, and the call returns 1 only after re-reading
the current Space and confirming it moved; `gt_window_raise` then re-resolves and raises the real
window. Every symbol is `dlsym`-resolved on the handle `space.m` already opens — no link flag, no
`#cgo` change, no `shim` edit (D24) — and none join `g_sl.loaded`'s minimum, so the read queries are
unaffected. A 0 return (symbols absent, Space unknown or already current, switch did not take) falls
through unchanged to P7.1. **Not verified: this host has one Space (D24); the cross-Space path is
reasoned from how yabai / Hammerspoon drive `CGSManagedDisplaySetCurrentSpace`, not observed — a
human confirms it under V6.2.** A macOS release that drops the symbols degrades this to P7.1's floor.

**P7.4 — the default is to keep an app-reachable window.** With P7.1 in place an un-raiseable window
still does something useful, so it stays on screen; P7.2 removes only what is actually gone. No
`prefs` field to hide app-only-reachable windows — add one only if that turns out to be wanted.
Recorded in `docs/ARCHITECTURE.md` under "The actionability rule".

---

## D48 · AltTab is credited as inspiration; the from-scratch claim is unchanged — 2026-09-08

**Decision (owner's call).** The project now names **AltTab** (`lwouis/alt-tab-macos`) as the prior
art that inspired it — the idea that macOS deserves a fast, preview-driven window switcher, and the
proof it can be done well. Earlier docs referenced "a mature open-source Swift switcher" without a
name (D7 deliberately kept prior art unnamed and self-contained; D10 measured "a reference switcher
v11.6.0" anonymously). That anonymity is dropped.

**What does not change.** GoTab still inherits nothing: no code, no assets, no bundle ID, no
preference or license continuity. Being *inspired by* an app and an idea is not the same as being
derived from it, and the wording everywhere is "inspired by", never "based on" or "port of". D7's
substance stands — `PLATFORM-LESSONS.md` remains a set of platform facts stated on their own terms
and measured independently against GoTab's spikes; it now says which switcher proved the problem
tractable rather than pretending none exists.

**Where it is recorded:** `README.md` (an Acknowledgements section), `docs/PLATFORM-LESSONS.md`
(the intro and §1), and the project website (`docs/site/`, served at `https://elgx.me/gotab/` and,
via the release workflow, at the GitHub Pages URL alongside `latest.json`). Both projects are
GPL-3.0-or-later, which is a shared licence choice, not a derivation.

## D49 · A project website lives in `docs/site/` and is served from two hosts — 2026-09-08

`docs/site/index.html` is a single self-contained page — inline CSS, no external requests, the app
icon copied in as `gotab-icon.png`, theme-aware, responsive. It is the short overview a user wants
before cloning: what GoTab is, the firm decisions behind it, how to install, the honest "early
release" status, and the AltTab acknowledgement (D48).

It is served at **`https://elgx.me/gotab/`** (deployed by the owner, outside CI) and the release
workflow also copies it into the Pages artifact, so **`https://moustafa-elgammal.github.io/gotab/`**
renders the same page while still serving `latest.json` next to it for `gotab -check-update`. One
source, two hosts; the version string in the page is updated by hand at release time (the download
button points at `releases/latest`, which never goes stale).

---

## D50 · V6.3 — summon → pixels is ~15 ms warm, 7x inside the 100 ms budget — 2026-09-08

The Phase 0 target "summon → pixels on screen < 100 ms" was budget-allocated but never timed end to
end on the assembled app: drawing ~1.3 ms (D13), capture ~46 ms and off the summon path by design
(D12), enumeration event-driven and not on the path either. V6.3 closes it.

**Instrument.** `gotab -timing` (scaffold, `docs/tasks/V6.3.md`): the full switcher pipeline —
`CreatePanel`, the event loop, the prefetcher, `panelRenderer` — minus the hotkey and the permission
gate, with a driver that arms a one-shot `CATransaction` completion block in `gt_panel_show` and
posts N summons through `Loop.Post`. Each sample is post → completion block (the frame handed to the
render server — the same edge `spike/panel` measured, D13), in Go's monotonic clock. Inert in the
shipped switcher: `g_timing_armed` starts 0 and `gotab -switch` never arms it.

**Measured, host macOS 26.6.2, both grants, 2–3 windows open, 30 summons:**

| | ms |
|---|---|
| cold (summon 1 after launch) | **74.24** |
| warm min / median / p95 / max (n=29) | 1.83 / 8.43 / **15.25** / 19.89 |

- **Warm p95 15.25 ms — PASS at ~15 % of budget.** 30/30 summons committed; no completion block
  ever timed out.
- The cold 74 ms is first-frame framework warm-up (CoreAnimation, first layer tree, font raster) —
  D13 saw ~14 ms for the bare spike; the assembled app's first frame is heavier but still inside.
  D5's "pay the cold cost at launch, not first summon" already covers this — the number is recorded,
  not treated as a fail.
- Median 8 ms with thumbnails present is itself evidence capture is **not** on the summon path: a
  freshly captured thumbnail is ~46 ms (D12), so an 8 ms summon cannot be capturing. The prefetcher
  does it ahead of time, as designed.

**What is still owed** (V6.3 stays "partial" until then): a control run with **Screen Recording
off**, to make the "capture is off the path" point by construction rather than by inference; and,
ideally, a 30–50-window run — the draw path scales gently with tile count (D13) but this run had
only 2–3 tiles. Neither is expected to move the verdict.

**End to end:** ⌥⇥ delivery is V6.1's 3.2 ms worst case (D45); ⌥⇥ → pixels ≈ 3.2 + 15.25 ≈ ~18.5 ms
p95, comfortably inside 100 ms. The Phase 0 summon budget is met.

---

## D51 · V6.10 — the switcher's per-tile accessibility is right; the container label is at risk — 2026-09-08

P5.2 built the panel's accessibility tree against the AppKit API and it had never been inspected on a
machine (D41). `gotab -switch -demo` (a new flag: skip the hotkey, script a summon that holds and
cycles) puts the panel up offscreen; its tree was walked with System Events — the
Accessibility-Inspector substitute V6.10's contract allows. **No VoiceOver: announcements were not
heard, so this is the structural half only.**

**Right, verified live:**

- Each tile is `AXButton` role.
- Each tile's label is exactly `"<title>, <app>"` — e.g. `"gotab, GoLand"`, `"New Message, Mail"`.
- Exactly one tile carries `AXSelected = true`, and it advances on every demo cycle (sampled three
  in a row: Chrome → GoLand → Mail), each with the correct label. The selected-child machinery
  fires; whether VoiceOver *speaks* it every time is the remaining human check.
- Tile press is a no-op (`accessibilityPerformPress` → `NO`), as V6.10 §6 wants.

**At risk — needs the VoiceOver run to settle:** the `"Window switcher"` label is set on
`GTTileView`, which returns `isAccessibilityElement = NO`. Live, the tile buttons are **direct
children of the panel window**, the window's `AXTitle` is **empty**, and its subrole is
`AXSystemDialog` — nothing in the tree carries the text "Window switcher". A screen reader may voice
the group through the parent chain or may land the user on an unlabelled system dialog. If it is
silent, the fix is P5.2's: put the label on the window/panel, or make the container a real element.

**Not done:** the `gotab -settings` control labels (System Events' `entire contents` hangs on that
window — Accessibility Inspector or VoiceOver needed), and every announcement. V6.10 stays
**partial**.

---

## D52 · The Finder-launch grant bug is real, and Phase 8 gives GoTab a surface — 2026-09-08

A user launched `/Applications/GoTab.app` from Finder: it ran, but ⌥⇥ did nothing. Reproduced and
pinned:

| launch | result |
|---|---|
| `/Applications/GoTab.app/Contents/MacOS/GoTab -switch` from a terminal | `switcher ready` — works |
| `open /Applications/GoTab.app` | runs, repeated `TCCAccessRequest` in the log, switcher never starts |

**Cause.** `open` / LaunchServices makes the app its own responsible process, identity `app.gotab`.
That copy had no Accessibility grant. A terminal launch is different — TCC attributes the permission
to the *responsible* process, and the terminal is commonly granted, so a direct exec borrows it
(PLATFORM-LESSONS §5). The bundle is ad-hoc signed (no Team ID), so its grant is tied to that exact
copy and does not carry over from a terminal run or a previous build. `ensurePermissions` then sits
in its 5-minute poll loop waiting for a grant the user was never clearly asked for.
**Fix for a user:** remove GoTab from Settings → Privacy → Accessibility and re-add
`/Applications/GoTab.app` (a stale ad-hoc entry does not work re-toggled); same for Screen Recording.

**Two bugs behind it, both filed now:**

1. **No non-terminal surface.** GoTab is `LSUIElement` — no Dock tile, no app menu — so Settings and
   Quit were reachable only as `gotab -flag` in a shell. → **P8.1** (this commit): an `NSStatusItem`
   with `GoTab <version>` / **Settings…** / **Quit GoTab**. "Settings…" spawns `gotab -settings` (the
   standalone entry point owns its window, run loop and Regular policy, so the switcher stays a clean
   Accessory); "Quit GoTab" cancels `ctx`. `menubar.{h,m,go}` + `runSwitcher` wiring, torn down in
   the shutdown goroutine. Verified live: icon in the menu bar, menu
   `[GoTab dev · Settings… · Quit GoTab]`, Settings opens, Quit exits code 0.
2. **Onboarding may be silent.** The permission `NSAlert` runs before `[NSApp run]` in an Accessory
   app and, on this `open` launch, did not visibly surface. → **P8.2** (open), settled with V6.9 /
   **V6.15**: force it with a run-loop spin, defer onboarding past `CreatePanel`, or route first-run
   through the menu.

Phase 8 — Reachability. Verification is V6.15 (a clean account).

---

## D53 · `capture.m` was compiling ~40 `-Wunguarded-availability-new` warnings; now it fails the build instead — 2026-09-08

A user's `./scripts/install.sh` printed ~40 warnings per architecture: every `SCShareableContent` /
`SCWindow` / `SCStreamConfiguration` / `SCContentFilter` / `SCScreenshotManager` use in `capture.m`
flagged as "available on macOS 12.3 (or 14.0) or newer, but the deployment target is macOS 12.0".

**They were real, in the sense that clang could not see the guard.** The design (D26) is: weak-link
ScreenCaptureKit, and `sck_present()` checks `NSClassFromString(@"SCScreenshotManager") != nil` at
runtime so a below-floor OS gets `GT_ERR_UNAVAILABLE` instead of a link failure. But
`-Wunguarded-availability-new` is a *static* check — a runtime function call is not a guard it
recognises. It wants a lexically-enclosing `if (@available(...))` or an `API_AVAILABLE` attribute.
Without one, the fallback rested on "messaging a NULL Objective-C class is a safe no-op", which is
true but undocumented-in-the-code and fragile.

Also: `go build` never showed these. It uses the SDK's own deployment target (26.0 here, D17), where
all the APIs exist. Only `build.sh`'s explicit `MACOSX_DEPLOYMENT_TARGET=12.0` triggers them — so the
gate was green and the noise only appeared on a release/install build.

**Fix.** `API_AVAILABLE(macos(12.3))` on `g_content` and the three SCK helpers; the SCK body of
`gt_capture` wrapped `if (@available(macOS 14.0, *)) @autoreleasepool { … }` with a trailing
`return GT_ERR_UNAVAILABLE` for < 14 (which `sck_present()` already returned — the branch is a
formality clang needs). Behaviour on macOS 14+ is byte-for-byte the same.

**And it now fails the build, not warns.** `shim.go` adds `-Werror=unguarded-availability-new`: this
package's whole below-floor story depends on every such call being guarded, so an unguarded one is a
bug. Caveat: it only bites under `build.sh` (minos 12.0); a plain `go build` at the SDK target still
cannot see the condition.

---

## D54 · The main loop was CFRunLoopRun, not -[NSApp run] — the app read as "Not Responding" and the menu bar was dead — 2026-09-08

A user running the installed `/Applications/GoTab.app`: ⌥⇥ switched windows fine, but Activity
Monitor showed **GoTab (Not Responding)** and clicking the menu-bar icon did nothing.

**One cause.** `runloop.m` ran `CFRunLoopRun()` on the main thread, chosen in D32 on the premise
that "nothing depends on NSApp's event-dispatch state" — true when a never-key `NSPanel` was the
only surface. `CFRunLoopRun()` *turns* the loop (so `darwin.OnMain`, the `NSWorkspace` observers, CA
commits and the appearance KVO all work, and the panel renders), but it never calls
`-[NSApp nextEventMatchingMask:]`, so the process never **dequeues** AppKit events. Two things need
that:

1. **The system's app-responsiveness check.** macOS flags a process "Not Responding" when its main
   thread stops servicing the event queue. The ⌥⇥ hotkey kept working throughout because it runs on
   its own dedicated thread with its own `CGEventTap` + `CFRunLoopRun` (D33), independent of the
   main thread — which is why the symptom looked cosmetic.
2. **The menu-bar status item (P8.1).** A click on an `NSStatusItem` is a mouse event delivered
   through `-[NSApp sendEvent:]`, which only runs inside `-[NSApp run]`'s loop. Under `CFRunLoopRun()`
   the click was never dispatched, so the menu never opened and "Settings…" never fired. The icon
   itself appeared because installing it only needs the main dispatch queue, which *was* serviced.

Same defect hit `gotab -settings`: its window (same `RunLoop()`) would have been non-interactive and
that process "Not Responding" too.

**Fix.** `gt_run_loop()` is now `-[NSApp run]`. It does its own `-finishLaunching` and per-turn
autorelease pool, so both were dropped; the shared `NSApplication` and its activation policy are
still set earlier by `gt_panel_create` / `gt_settings_open`. `gt_run_loop_stop()` can no longer be
`CFRunLoopStop` from another thread — it now hops to the main queue and calls `[NSApp stop:]` plus a
no-op `NSEventTypeApplicationDefined` event (`-stop:` only takes effect after `-run` processes one
more event, and an idle `-run` sits in `nextEventMatchingMask:` on `distantFuture`). A `g_running`
flag keeps a stop with no loop running a clean no-op.

**Verified on this machine** (built `.app`, both grants):

- `-switch` stayed responsive to AX/AppleEvent queries (~0.4 s each) across 50 s of runtime; under
  the bug the same query blocks for the full 6 s messaging timeout within seconds of launch.
- SIGINT still tears `-switch` down cleanly (the new `gt_run_loop_stop` path).
- `gotab -settings` shows "GoTab Settings", its controls respond to synthetic clicks, and closing
  the window exits the process (`windowWillClose:` → `goSettingsClosed` → `StopRunLoop`).
- The live menu-bar *click* still wants a human (V6.15); the mechanism it was missing — event
  dispatch — is what this restores.

Gate green, no tests (D16 / D23).

---

## D55 · First-run onboarding: one combined ask, shown once, and it never blocks the switcher — 2026-09-08

The Finder-launch failure D52 pinned had two halves. D54 fixed the run loop; this is the onboarding
half (**P8.2**). Before this, `gotab -switch` called `ensurePermissions` **before** `CreatePanel` and
the run loop: if Accessibility was missing it showed an `NSAlert`, then sat on the main thread in a
5-minute `CheckPermissions` poll. Two things were wrong with that.

1. **The alert did not reliably surface.** An Accessory app (`LSUIElement`) that has not started
   `-[NSApp run]` cannot bring a modal forward — `activateIgnoringOtherApps:` has nothing to activate
   yet. A first-timer got a background process and no dialog (D52, observed).
2. **It blocked everything.** The menu bar (P8.1) and the panel came up *after* the gate, so while
   the poll ran the switcher had no surface at all — Activity Monitor's "Not Responding" (D54) on top
   of a window the user could not find or quit. And "Screen Recording missing" never showed a dialog
   at all — it was a one-line stderr note, invisible from Finder.

**What it is now.**

- **`onboardPermissions` runs after `CreatePanel`, and the modal is deferred onto the run loop.** The
  Finder path does `darwin.OnMain(func() { PromptPermissions(needAX, needSR, canDefer=true) })`, so
  the alert fires on `-[NSApp run]`'s first turn, when the app *can* come forward. `gt_permissions_prompt`
  now also saves and restores the activation policy around `runModal` (it goes Regular for the
  modal's life, back to Accessory after) so no Dock tile lingers behind the already-created panel.
- **One combined ask, both grants, phrased for what each buys:** *Accessibility — to enumerate
  windows and raise the one you pick; Screen Recording — for window titles and the live thumbnails.*
  "AX present, SR missing" now shows the dialog too, where before it was a silent stderr line.
- **Shown once.** A bool `PermissionsOnboarded` in the CFPreferences domain (app state, not a
  user setting — it stays out of `internal/prefs`' schema) gates the modal. It is *cleared* whenever
  `perm.OK()`, so a later revoke re-arms the one-time prompt. The dismiss button is **"Not Now"**,
  not "Quit" (`can_defer`); `gotab -permissions` keeps "Quit".
- **Nothing blocks.** `ensurePermissions` and its 5-minute poll are gone. `runSwitcher` reads the
  grants once and always brings the switcher up. `armSwitchHotkey` tries `StartHotkey`; on
  `ErrNotTrusted` it starts a 750 ms background poll that arms ⌥⇥ the moment Accessibility lands —
  D36's no-relaunch recovery, off the main thread. A mid-session grant still needs no relaunch; a
  never-granted session is a live menu bar (Settings, Quit) rather than a hung process.
- The TTY path is unchanged (D36): from a shell, print the `x-apple.systempreferences:` deep links,
  no modal.

**Verification** is still **V6.15** (a clean account, `open /Applications/GoTab.app`): the combined
alert appears on first launch and not on the second, "Open System Settings" opens the pane(s),
granting brings ⌥⇥ up with no relaunch, and the switcher is responsive with its menu bar throughout.
Gate green, `build.sh` clean at minos 12.0, no tests (D16 / D23).
