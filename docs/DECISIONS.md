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
