# Notification-Based History Sync (MVP) Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add an optional local daemon mode that performs initial historical sync, registers Gmail watch, then continuously applies incremental history sync when Pub/Sub notifications arrive.

**Architecture:** Reuse existing `SyncHistoryWithDB` as the single source of truth for stateful incremental application. Add a watch runtime loop that (1) ensures a valid Gmail watch exists, (2) long-polls a Pub/Sub pull subscription, and (3) triggers history catch-up on each notification (or heartbeat interval). Persist watch metadata in SQLite `sync_state` to survive restarts.

**Tech Stack:** Go, existing Gmail API client (`google.golang.org/api/gmail/v1`), SQLite state (`.outtake.v2.sqlite`), Google Cloud Pub/Sub client (`cloud.google.com/go/pubsub`).

---

### Task 1: Add CLI flags for notification daemon mode

**Files:**
- Modify: `main.go`
- Test: `main.go` (covered by integration/manual command verification for CLI wiring)

**Step 1: Add failing invocation expectations (manual smoke contract)**

Document expected behavior in comments/TODO near CLI setup:
- `--watch` requires `--gcp-project` and `--pubsub-subscription`.
- Without `--watch`, existing behavior is unchanged.

**Step 2: Run current tests as baseline**

Run: `go test ./...`
Expected: PASS (baseline before flag changes)

**Step 3: Implement minimal CLI flags and validation**

In `main.go`, add:
- `--watch` (bool)
- `--gcp-project` (string)
- `--pubsub-subscription` (string)
- `--watch-poll-timeout` (duration-like seconds int, default e.g. 60)
- `--watch-lease-renew-before-sec` (int, default e.g. 86400)

Validation:
- if `--watch` and required fields missing, return clear error.

**Step 4: Re-run tests**

Run: `go test ./...`
Expected: PASS

**Step 5: Commit**

```bash
git add main.go
git commit -m "feat(cli): add watch mode flags for notification sync"
```

---

### Task 2: Extend Gmail service abstraction to support users.watch

**Files:**
- Modify: `lib/gmail/service.go`
- Modify: `lib/gmail/gmail_test.go` (fake service)
- Test: `lib/gmail/*_test.go`

**Step 1: Write failing test for service-level watch call**

Add test that fake service/runtime can return watch response (`historyId`, `expiration`) and that caller consumes it.

**Step 2: Run targeted test to verify failure**

Run: `go test ./lib/gmail -run Watch -v`
Expected: FAIL (method missing)

**Step 3: Add interface + implementation**

In `lib/gmail/service.go`:
- extend `gmailService` with `Watch(labelId string, topicName string) (*gmail.WatchResponse, error)`
- implement in `restGmailService` via `s.svc.Watch("me", req).Do()` using label filter when provided.

**Step 4: Update test fakes/stubs**

Implement `Watch(...)` in test fake service used across `lib/gmail` tests.

**Step 5: Re-run package tests**

Run: `go test ./lib/gmail -v`
Expected: PASS

**Step 6: Commit**

```bash
git add lib/gmail/service.go lib/gmail/gmail_test.go lib/gmail/*_test.go
git commit -m "feat(gmail): add users.watch support to service abstraction"
```

---

### Task 3: Persist watch runtime state in SQLite sync_state

**Files:**
- Modify: `lib/gmail/list_pages_schema.go` (or schema owner for `sync_state` helpers)
- Modify: `lib/gmail/history_sync.go` (shared state helpers if appropriate)
- Create: `lib/gmail/watch_state.go`
- Create: `lib/gmail/watch_state_test.go`

**Step 1: Write failing state tests**

Tests for read/write helpers:
- set/get watch expiration (unix ms)
- set/get watch history baseline
- clear watch state

**Step 2: Run targeted tests to verify failure**

Run: `go test ./lib/gmail -run WatchState -v`
Expected: FAIL (new file/functions not present)

**Step 3: Implement minimal state helpers**

Create keys in `sync_state` (constants):
- `watch_expiration_unix_ms`
- `watch_last_register_history_id`
- `watch_topic`
- `watch_subscription` (optional audit)

Provide helpers:
- `getWatchState(db)`
- `setWatchState(db, ...)`
- `clearWatchState(db)`

Use existing `getSyncState` / transactional `setSyncState` style.

**Step 4: Re-run tests**

Run: `go test ./lib/gmail -run WatchState -v`
Expected: PASS

**Step 5: Commit**

```bash
git add lib/gmail/watch_state.go lib/gmail/watch_state_test.go lib/gmail/list_pages_schema.go lib/gmail/history_sync.go
git commit -m "feat(state): add persisted watch sync state in sqlite"
```

---

### Task 4: Implement watch registration + renewal logic

**Files:**
- Create: `lib/gmail/watch_register.go`
- Create: `lib/gmail/watch_register_test.go`
- Modify: `lib/gmail/service.go` (if request shaping needs helper)

**Step 1: Write failing tests for renewal decisions**

Test cases:
- no existing expiration => register
- expiration near threshold => renew
- expiration far in future => skip renew
- watch call failure bubbles error

**Step 2: Run targeted tests to verify failure**

Run: `go test ./lib/gmail -run WatchRegister -v`
Expected: FAIL

**Step 3: Implement registration function**

Add function (example):
- `func (g *Gmail) EnsureWatchWithDB(db *sql.DB, topic string, renewBefore time.Duration) error`

Behavior:
- load watch state
- decide renew/skip
- call `g.svc.Watch(...)`
- persist expiration + returned `historyId` atomically

**Step 4: Re-run tests**

Run: `go test ./lib/gmail -run WatchRegister -v`
Expected: PASS

**Step 5: Commit**

```bash
git add lib/gmail/watch_register.go lib/gmail/watch_register_test.go lib/gmail/service.go
git commit -m "feat(watch): add watch registration and renewal logic"
```

---

### Task 5: Implement Pub/Sub pull loop that triggers history catch-up

**Files:**
- Create: `lib/gmail/watch_loop.go`
- Create: `lib/gmail/watch_loop_test.go`
- Modify: `go.mod` / `go.sum`

**Step 1: Write failing loop tests with fake receiver**

Add tests for:
- on notification message => call `SyncHistoryWithDB`
- on malformed payload => ack and continue
- on sync error => nack/retry behavior (or log+continue based on chosen policy)
- periodic fallback tick triggers `SyncHistoryWithDB` even with no notifications

**Step 2: Run targeted tests**

Run: `go test ./lib/gmail -run WatchLoop -v`
Expected: FAIL

**Step 3: Implement loop with dependency injection**

Add small abstraction to avoid hard-coding pubsub client in tests:
- receiver interface (`Receive(ctx, handler)` style)
- production adapter using `cloud.google.com/go/pubsub`

Loop behavior (MVP):
- ensure watch before entering loop
- receive messages, ack after successful history sync trigger
- debounce/coalesce bursts (simple in-flight mutex/flag)
- periodically re-run `EnsureWatchWithDB`

**Step 4: Add dependency**

Run: `go get cloud.google.com/go/pubsub@latest`

**Step 5: Re-run tests**

Run: `go test ./lib/gmail -run WatchLoop -v`
Expected: PASS

**Step 6: Commit**

```bash
git add lib/gmail/watch_loop.go lib/gmail/watch_loop_test.go go.mod go.sum
git commit -m "feat(watch): add pubsub pull loop driving incremental history sync"
```

---

### Task 6: Wire daemon mode in main runtime

**Files:**
- Modify: `main.go`
- Modify: `README.md`
- Create: `docs/watch-mode.md`

**Step 1: Write failing integration-style test plan (manual command checks)**

Define command contracts in docs/comments:
- non-watch path: one-shot existing flow
- watch path: bootstrap list/message/history then enter loop

**Step 2: Run baseline tests**

Run: `go test ./...`
Expected: PASS before wiring changes

**Step 3: Implement runtime branch**

In `main.go`:
- if `--watch=false`: keep current one-shot behavior
- if `--watch=true`:
  1. run `SyncListPagesWithDB`
  2. run `SyncListedMessagesWithDB`
  3. run `SyncHistoryWithDB`
  4. enter `RunWatchLoopWithDB(...)`

Include graceful shutdown with context cancellation on SIGINT/SIGTERM.

**Step 4: Document usage**

Update `README.md` and add `docs/watch-mode.md` with:
- required GCP resources (topic/subscription)
- IAM expectations for Gmail watch + Pub/Sub pull
- sample command line

**Step 5: Re-run full test suite**

Run: `go test ./...`
Expected: PASS

**Step 6: Commit**

```bash
git add main.go README.md docs/watch-mode.md
git commit -m "feat: wire notification-based watch daemon mode"
```

---

### Task 7: End-to-end verification checklist (pre-merge)

**Files:**
- Modify: `docs/watch-mode.md`

**Step 1: Local dry run (no watch)**

Run:
```bash
go run . --directory /tmp/outtake-maildir
```
Expected: one-shot sync behavior unchanged.

**Step 2: Local watch mode validation**

Run with real config:
```bash
go run . \
  --directory /tmp/outtake-maildir \
  --watch \
  --gcp-project <project-id> \
  --pubsub-subscription <subscription-id>
```
Expected: initial sync then continuous loop; logs show watch registration and history cursor advancement.

**Step 3: Restart resilience check**

- Stop process, restart command.
- Expected: state is reused from SQLite; no forced full resync unless history token expired.

**Step 4: Expired history simulation (optional)**

- Force stale cursor in DB.
- Expected: 404 fallback path runs listing + download and recovers.

**Step 5: Final test run**

Run: `go test ./...`
Expected: PASS

**Step 6: Commit docs polish**

```bash
git add docs/watch-mode.md
git commit -m "docs: add watch mode verification and operations notes"
```
