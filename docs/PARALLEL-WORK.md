# Working with agents on this repo

How to parallelise this build without burning tokens or creating merge chaos.

## The one rule that decides everything

**Fan out only where the work is genuinely independent.** In this project that is *Phase 1 and nothing
else*. Phase 1 tasks are pure Go, share no state, touch no cgo, and each lands in its own file. Phases 2
and 3 share the C shim and the main thread — parallel agents there produce conflicts that cost more to
untangle than the work saved. **Phase 2/3 get one owner, working sequentially.**

Spawning an agent costs a cold start: it re-derives context you already hold. That cost is worth paying
for an isolated, well-specified task with a clear acceptance test. It is never worth paying to "have a
second opinion" or to do work you could do inline.

## Worktrees

One worktree per task, so agents never share a checkout:

```bash
scripts/wt.sh new P1.3        # git worktree add ../gotab-wt/P1.3 -b feat/P1.3
scripts/wt.sh list
scripts/wt.sh done P1.3       # merge to main, remove worktree, prune branch
```

Convention: worktrees live in `../gotab-wt/<TASK-ID>`, branch `feat/<TASK-ID>`. Never two agents in one
worktree. Never an agent on `main`.

## Connected context — how agents share understanding

The problem: each fresh agent knows nothing, and re-reading the repo to catch up is exactly where tokens
disappear. The fix is that **context lives in the repo as small, stable, versioned files**, not in
conversation history.

Four files, in the order an agent reads them:

| file | size | role |
|---|---|---|
| `docs/ARCHITECTURE.md` | ~150 lines | invariants. Read by every agent, every time. Keep it small — its cost is multiplied by every spawn. |
| `docs/tasks/<ID>.md` | ~40 lines | **the task contract**: inputs, outputs, acceptance test, files it may touch |
| `internal/core/api.go` | frozen | the type/interface contract. Agents code against it and never change it. |
| `docs/DECISIONS.md` | append-only | why things are the way they are. Agents **append**, never rewrite. |

An agent reads those four and nothing else. If it needs more, the task was scoped wrong — that is a signal
to re-scope, not to go exploring.

**Interface-first is what makes this work.** `P1.0` freezes `api.go` before any Phase 1 agent starts. After
that, agents implementing P1.2 and P1.4 never need to know the other exists: they both code against the
same frozen types. No cross-agent coordination, no shared context, no merge conflicts.

## Task contract template

`docs/tasks/<ID>.md`:

```markdown
# <ID> — <title>

**Branch:** feat/<ID>   **Phase:** N   **Depends on:** <IDs, or none>

## Goal
One paragraph. What exists when this is done.

## Files you may touch
- internal/core/foo.go
- internal/core/foo_test.go
Nothing else. If you need to change api.go, stop and escalate.

## Acceptance
- [ ] `go test ./internal/core/... -run Foo` passes
- [ ] `go test -benchmem` reports 0 allocs/op on the hot path
- [ ] <the measurable thing>

## Notes
Anything non-obvious. Link to DECISIONS.md entries rather than restating them.
```

## Token discipline

Concrete tactics, roughly in order of how much they save:

1. **Scope tasks so the contract is the only context.** One task file + `ARCHITECTURE.md` ≈ 200 lines. An
   agent that reads the repo instead reads thousands. This is the whole game.
2. **Don't spawn for serial work.** Phases 2/3 inline. The spawn overhead exceeds the parallelism gain.
3. **Keep `ARCHITECTURE.md` under ~200 lines.** Every agent pays for it. Prose that isn't an invariant
   belongs in `DECISIONS.md`, which agents read only when relevant.
4. **`go doc ./internal/core` instead of reading source.** Signatures and doc comments, not bodies.
5. **`rg -l` then read line ranges.** Never read a whole file to find one function.
6. **Commit small and often.** A later agent reads `git log --oneline` and one diff, not the tree.
7. **Report back deltas, not narration.** An agent's final message should be: what changed, what the test
   says, what surprised it. Not a retelling of its process.
8. **Batch the gate checks.** Run `scripts/check.sh` once at the end rather than a tool call per assertion.

## Definition of done

A task is done when, and only when:

1. Its acceptance boxes in `docs/tasks/<ID>.md` are ticked
2. `scripts/check.sh` is green (`go vet`, `go test ./...`, `gofmt -l` empty)
3. Its box in `docs/ROADMAP.md` is `[x]` **in the same commit**
4. Anything surprising is appended to `docs/DECISIONS.md`

Step 3 is what makes a cold session resumable. Skipping it is how this project gets lost.
