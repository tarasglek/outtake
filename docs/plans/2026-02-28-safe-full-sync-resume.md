# Safe Full-Sync Resume Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Make interrupted initial/full sync resumable without risking skipped messages or false completion.

**Architecture:** Do not resume from `history_index_progress` during full sync. Instead, add a dedicated resumable-full-sync state machine that checkpoints Gmail list pagination progress and tracks seen message IDs in persistent storage. Only switch to incremental (`history_index`) after full sync has fully completed, including delete reconciliation.

**Tech Stack:** Go, BoltDB, Gmail API (`Users.Messages.List`, `Users.Messages.Get`, `Users.History.List`)

---

### Task 1: Lock in regression tests for unsafe history-progress resume

**Files:**
- Modify: `lib/gmail/gmail_test.go`

**Step 1: Write failing test**
- Add a test that simulates partial full sync where a high history ID is observed before full mailbox completion.
- Assert `Sync(false, nil)` must not treat `history_index_progress` as a valid incremental resume source.

**Step 2: Run test to verify failure**
- Run: `go test ./lib/gmail -run Test.*Progress.* -v`
- Expected: FAIL on current behavior if still preferring progress index.

**Step 3: Implement minimal fix (temporary safety gate)**
- Keep/restore behavior: ignore `history_index_progress` for resume until full-sync checkpoints are safe.

**Step 4: Re-run test**
- Run: `go test ./lib/gmail -run Test.*Progress.* -v`
- Expected: PASS.

**Step 5: Commit**
- `git commit -m "test(sync): guard against unsafe history progress resume"`

---

### Task 2: Add persistent full-sync session state in cache

**Files:**
- Modify: `lib/gmail/cache.go`
- Modify: `lib/cache.go` (if helper methods needed)
- Test: `lib/gmail/gmail_test.go`

**Step 1: Write failing tests**
- Add tests for round-trip of new keys:
  - `full_sync_active` (bool/int marker)
  - `full_sync_page_token` (string)
  - `full_sync_seen` bucket entries (message IDs)
  - `full_sync_highest_history` (uint64)

**Step 2: Verify failure**
- Run targeted tests and confirm compile/runtime failure before implementation.

**Step 3: Implement minimal cache APIs**
- Add typed getters/setters/clearers for full-sync session keys.
- Add `AddFullSyncSeen(id)` and iterator/clear for seen bucket.

**Step 4: Re-run tests**
- Ensure all new cache tests pass.

**Step 5: Commit**
- `git commit -m "feat(cache): add persistent full-sync session state"`

---

### Task 3: Convert full sync into resumable state machine

**Files:**
- Modify: `lib/gmail/gmail.go`
- Test: `lib/gmail/gmail_test.go`

**Step 1: Write failing integration-style tests**
- Test scenario A: interrupt during full sync after N pages, rerun, verify list resumes from saved page token and continues.
- Test scenario B: ensure delete reconciliation uses persisted seen set and only runs after listing+processing complete.
- Test scenario C: ensure `history_index` is committed only on successful full completion.

**Step 2: Verify RED**
- Run: `go test ./lib/gmail -run TestFullSyncResume -v`
- Expected: FAIL.

**Step 3: Implement minimal resumable flow**
- On full sync start:
  - if `full_sync_active`, resume from saved page token and seen bucket;
  - else initialize session state.
- During listing:
  - persist page token after each page;
  - persist seen IDs for each message ID encountered.
- During processing:
  - continue existing op pipeline.
- On completion:
  - run delete reconciliation using persisted seen IDs;
  - set committed `history_index` from max observed history;
  - clear full-sync session state atomically (same run).

**Step 4: Re-run tests**
- Targeted and package tests should pass.

**Step 5: Commit**
- `git commit -m "feat(sync): add resumable full sync session state machine"`

---

### Task 4: Improve diagnostics and operator clarity

**Files:**
- Modify: `lib/gmail/gmail.go`
- Modify: `ARCHITECTURE.md`

**Step 1: Add diagnostics tests (or golden log assertions where practical)**
- Verify logs include:
  - full sync fresh vs resumed
  - page token progress
  - seen count
  - completion/finalization

**Step 2: Implement logs**
- Add explicit log lines for state transitions and counters.

**Step 3: Document behavior**
- Update `ARCHITECTURE.md` with “resumable full sync” semantics and safety invariants.

**Step 4: Verify tests/docs sanity**
- `go test ./...`

**Step 5: Commit**
- `git commit -m "docs(sync): document resumable full sync invariants"`

---

### Task 5: Verification and cleanup

**Files:**
- No new source files expected

**Step 1: Run full verification**
- `go test ./...`

**Step 2: Optional manual failure-injection run**
- Start full sync on test mailbox, interrupt mid-run, restart, verify resume logs and eventual consistency.

**Step 3: Confirm no unsafe resume path remains**
- Ensure `history_index_progress` is either removed or retained only for observability (never resume source).

**Step 4: Final commit (if needed)**
- `git commit -m "chore(sync): finalize safe resume validation"`
