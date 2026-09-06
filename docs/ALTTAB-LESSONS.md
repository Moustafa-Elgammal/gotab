# Prior art: what AltTab learned

GoTab is a clean-slate rewrite, but the *problem* is not new. AltTab is ~57,000 lines of Swift and years of
reverse-engineering against undocumented macOS behaviour. This file distils the design conclusions that
cost the most to discover, so GoTab does not pay for them twice.

**Source:** the AltTab repo, <https://github.com/lwouis/alt-tab-macos>. Clone it wherever you like and
`/add-dir` that checkout to read the originals — nothing in GoTab depends on where it sits. Every claim
here was read out of that codebase on 2026-09-06, with the file named so you can go verify. Where AltTab
records a measurement, the measurement is reproduced rather than paraphrased.

**How to use this:** read the relevant section before designing a subsystem, not after. Most entries
describe a constraint that looks removable until it isn't.

---

## 1 · Scale reference

What a complete implementation actually costs, measured on the AltTab tree:

| | |
|---|---|
| total Swift | 56,655 LOC |
| files importing AppKit/Cocoa | 151 files, 30,863 LOC (**54%**) |
| `NSView`/`NSWindow` subclasses | 53 |
| files touching Accessibility | 40 |
| files touching SkyLight/CGS private API | 32 |
| pure decision kernels (the portable part) | 42 files, 7,825 LOC (**14%**) |
| localizations | 22 |
| vendored ObjC frameworks | 3 (Sparkle, AppCenter, ShortcutRecorder) |

The shape matters more than the totals: **86% of the code is platform glue, 14% is decisions.** GoTab's
`internal/core` corresponds to that 14%. Budget accordingly — the glue is where the time goes.

---

## 2 · The two-plane architecture (the central insight)

No single macOS API answers both "what windows exist" and "which one is the user looking at", and either
source can be unavailable. AltTab therefore splits them and **never lets one stand in for the other**.

**Physical plane — the WindowServer (SkyLight/CGS).** Existence, geometry, z-order/level, Space
membership, ordered-in, minimize, fullscreen, a fallback title. Sourced from an SLS notify-proc tap plus
batched queries. Crucially, **it stays available when an app is wedged.**

**Semantic plane — one app-level AX observer per process.** Focused/main-window notifications and title
changes. **There is no observer per window** — that was a deliberate choice to avoid observer churn.
AX reads remain for discovery-time subrole, user-visible title, tab detection, and the initial focus seed.

Source: `src/windowserver/README.md`, `src/window-tracking/AttentionOrderSpecs.md`.

> Accessibility permission is required regardless. Focusing another app's window is permission-gated
> whether you go through AX or SLS. The win from the split is *reliability*, not fewer permissions.

### There is no `wid → AXUIElement` lookup

Confirmed by reverse-engineering: `_AXUIElementGetWindow` is element→wid only. There is no reverse
routine, no window-by-id parameterized attribute, and the remote token carries an opaque app-internal id,
not a wid. So an AX element for an off-Space window can generally only be obtained by enumerate-and-match.

AltTab's mitigations, worth copying:
- elements are acquired **lazily and cached**
- a periodic inventory groups missing wids **by process**, so one batched read resolves many
- `kAXFocusedWindow` and `kAXMainWindow` are the two window attributes AppKit does **not** put behind the
  Space filter — they name an off-Space root for free
- a targeted brute-force shares a **250 ms budget** across whatever remains
- every AX notification arrives *holding the element* of the window it concerns, and nothing in AppKit's
  posting path consults a Space — so adopting that element is free discovery. It only reaches windows that
  speak, so it shrinks the brute-force population rather than replacing it.

---

## 3 · What may move the window order

Only two things may write MRU order, and AltTab funnels both through a single function that takes a source
tag naming which one authorised it:

1. **an attention decision** — a click naming its target, the switcher's own switch, or an app answering
   which window it considers focused. The only source that may claim *the user moved*.
2. **a structural repair** — the front window closed and something must take its place. Not a claim about
   the user at all.

**Nothing else.** In particular the WindowServer's own order/focus notifications (808/815/816) may *not*
move MRU: measured across twelve scenarios, they never named a window AX hadn't already named, always
named it later, and on activation fired once per on-Space window — which is a *set*, not an answer.

> This is the single most valuable rule in the file. A design that lets z-order events move MRU will feel
> subtly wrong in ways that are very hard to trace back.

### Timing: the 60 ms settle

An app raising all its windows answers focus once per window, each answer true, and the run ends where it
started. AltTab collapses a process's burst to its last answer over **60 ms** — the only arbitration delay
in the attention path. Evidence order is captured *before* the delay, so a click arriving mid-settle stays
newer. Widening it is not free: it delays every genuine switch equally, against a **measured floor of
219 ms for the fastest human action ever captured**.

---

## 4 · Window lifecycle: an element ending ≠ a window ending

AltTab tracks six states (`unverified`, `alive`, `axElementEnded`, `replacementPending`, `surfaceEnded`,
`confirmedClosed`) because the AX element and the WindowServer surface die independently.

- `AXUIElementDestroyed` → `axElementEnded`, and a fresh element for the same wid **heals it back to alive**
- an SLS 804 retires the surface but keeps a **two-second semantic retirement record** (MRU time, creation
  order, thumbnail, group membership). A new wid may inherit those facts only if it belongs to the same
  process *and* its AX element is explicitly equal.
- a focus that happened after retirement prevents the replacement from stealing the front
- process exit clears both immediately

Source: `src/switcher/state/WindowLifecycleSpecs.md`.

---

## 5 · macOS traps that will bite GoTab

Each of these cost AltTab a real bug. They are platform facts, so they transfer unchanged.

**Ordinary AppKit calls can be synchronous XPC.** `searchField.stringValue = ""` froze a user's main
thread for **3.0 seconds**: it resigned first responder → deactivated the system text-input context →
timed out. Assume anything that changes first responder, orders a window, or resizes one talks to another
process. Source: AltTab `AGENTS.md`, issue #5981.

**TCC judges the *responsible* process, not the binary.** A binary launched straight from a shell is judged
by the **terminal's** grants — it reads `accessibility:notGranted screenRecording:notGranted` and silently
does nothing, however it is signed. AltTab launches via `open` so LaunchServices makes the app its own
responsible process. **GoTab's dev-run script must do the same** or you will chase a permissions ghost.
Source: `ai/run.sh`.

**CoreAnimation commits at the end of the runloop turn.** Setting `alphaValue` or calling `orderOut` does
not reach the screen before the turn ends, so blocking later in the same turn keeps the stale frame up.
Defer and re-check state in the deferred block.

**`NSWorkspace`/`NSRunningApplication` are thread-safe** — their headers say so. LaunchServices reads
(`runningApplications`, `frontmostApplication`, `bundleURL`, `icon`, `activate`) may run off-main. Nearly
everything else in AppKit (`NSResponder`, `NSCell` and their subtrees) is main-thread-only and asserts.

**Exiting with captures in flight makes macOS prompt the user.** AltTab drains in-flight screenshots on
exit (up to 5 s) specifically to avoid spurious screen-recording permission dialogs. With ScreenCaptureKit
(GoTab's only option — see D3) this will apply too. Source: `src/main.swift`.

**A blocked signal mask makes the app unquittable.** `posix_spawn` hands the child the signal mask of the
spawning thread, so a launcher spawning from a libdispatch worker starts the app with TERM/INT/HUP pending
forever — only SIGKILL ends it, which is exactly the path that skips the capture drain. AltTab explicitly
unblocks them. Measured: same binary outlived every TERM when spawned from a blocked-mask parent; exited
in 0.26 s when unblocked.

---

## 6 · Latency discipline

- On the critical paths — summoning, a keystroke while cycling or searching, dismissal with focus —
  **nothing that can stall may run before the work the user is waiting for.** Put the visible work first,
  bookkeeping after.
- The attention decision lands **inside the same dispatch that produced it**. Deferring it one runloop turn
  lets the switcher draw one frame with the stale order, which AltTab's QA scores as "right, but late"
  rather than right.
- AltTab instruments this rather than guessing: a `MainThreadStall.step()` marker names the function in the
  log when it runs long. GoTab should build the equivalent early — see D4 for how badly naive instruments
  can mislead.

---

## 7 · Observability ceilings — do not design against these

Things that are genuinely impossible. Recognise them so you don't burn a week inventing an arbitration
rule for a signal that does not exist.

- **An in-app click inside an already-front, wedged app is unobservable.** The measurements produced no
  type-13, AX, WindowServer, or workspace event. No provider priority fixes this.
- **A programmatic activation of a wedged app names only its process**, never a window.
- **If a custom app retains both its AX element and its surface, posts no transition, and the user action
  wasn't observed, you cannot know the window closed.** No extra same-subsystem query manufactures the fact.
- **No observed API publishes a complete tab-membership set.** A focused/main notification names only the
  selected window. Membership is necessarily composite: AXTabGroup reads where apps expose it, plus
  physical fallback for fullscreen/inactive/unresponsive cases.

AltTab's stated discipline, worth adopting verbatim: **the engine never invents an attention edge it did
not observe.** When an activation names no window and AX never answers, the order does not move. A
worse-looking but safer answer beats guessing from z-order.

---

## 8 · What *not* to copy

- **53 `NSView` subclasses.** Sensible in Swift, actively harmful through cgo — every AppKit-invoked method
  becomes a C→Go callback at ~39 ns (D1). GoTab draws all tiles in **one** view (task P3.1).
- **A thumbnail retained per window, indefinitely.** `Window.swift:40` holds `var thumbnail:
  CALayerContents?` for the window's lifetime. This is the memory behaviour GoTab exists to improve on, via
  a bounded LRU. It is a policy difference, not a language one.
- **Legacy `SecKeychain*` or `kSecAccessControl`** in any licensing code — both can trigger Keychain
  password prompts. (Moot for GoTab today, which has no licensing.)
- **`CGWindowListCreateImage`** — obsoleted in macOS 15. See D3.
