# IMAP Notifications Triggered History Sync (INBOX-only) Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Replace the Pub/Sub notification direction with a simpler local mode that uses Gmail IMAP IDLE on INBOX only as a wake-up trigger, then runs existing Gmail API `SyncHistoryWithDB` for actual sync.

**Architecture:** Keep Gmail API + SQLite history pipeline as the single source of truth. Add a lightweight IMAP IDLE listener that reconnects safely and emits “sync now” signals. `--imap-notifications` implies long-running mode (no separate `--daemon` flag).

**Tech Stack:** Go, existing Gmail API client, SQLite state, IMAP client library with IDLE support (e.g. `github.com/emersion/go-imap` + `github.com/emersion/go-imap-idle`).

---

### Task 1: Remove Pub/Sub/watch artifacts added in previous iteration

**Files:**
- Modify: `main.go`
- Delete: `lib/gmail/watch_pubsub.go`
- Delete: `lib/gmail/watch_loop.go`
- Delete: `lib/gmail/watch_loop_test.go`
- Delete: `lib/gmail/watch_register.go`
- Delete: `lib/gmail/watch_register_test.go`
- Delete: `lib/gmail/watch_state.go`
- Delete: `lib/gmail/watch_state_test.go`
- Modify: `lib/gmail/service.go` (remove `Watch` API surface)
- Modify: `lib/gmail/gmail_test.go` (remove fake watch members)
- Modify: `lib/gmail/list_pages_schema.go` (remove watch sync_state keys)
- Modify: `README.md` (remove Pub/Sub usage)
- Modify: `go.mod`, `go.sum` (drop Pub/Sub dep if unused)

**Step 1: Write failing/guard checks for cleanup**

```bash
rg -n "RunWatchLoopWithDB|EnsureWatchWithDB|sync\.watch|pubsub|--watch|--gcp-project|--pubsub" .
```

**Step 2: Remove watch runtime and related APIs**

- Remove watch flags/branching from `main.go`.
- Remove `Watch(...)` from `gmailService` and implementations/fakes.
- Remove all watch-specific files and tests.

**Step 3: Remove dependency artifacts**

- Ensure no imports of `cloud.google.com/go/pubsub` remain.
- Run `go mod tidy`.

**Step 4: Verify cleanup**

```bash
rg -n "RunWatchLoopWithDB|EnsureWatchWithDB|sync\.watch|cloud\.google\.com/go/pubsub|--watch|--gcp-project|--pubsub" .
go test ./...
```

**Step 5: Commit**

```bash
git add -A
git commit -m "refactor: remove pubsub watch-mode artifacts"
```

---

### Task 2: Add `--imap-notifications` CLI mode (implies long-running)

**Files:**
- Modify: `main.go`

**Step 1: Define CLI contract**

- `--imap-notifications` enables long-running notifications mode.
- Without it: existing one-shot sync behavior.

**Step 2: Add flags**

In `main.go`, add:
- `--imap-notifications` (bool)
- `--imap-host` (default `imap.gmail.com:993`)
- `--idle-folder` (default `INBOX`)
- `--idle-debounce-sec` (default e.g. 3)
- `--notifications-heartbeat-sec` (fallback periodic sync, e.g. 300)

**Step 3: Run tests**

```bash
go test ./...
```

**Step 4: Commit**

```bash
git add main.go
git commit -m "feat(cli): add --imap-notifications mode flags"
```

---

### Task 3: Introduce IMAP IDLE trigger component (INBOX-only)

**Files:**
- Create: `lib/gmail/imap_idle_trigger.go`
- Create: `lib/gmail/imap_idle_trigger_test.go`
- Modify: `go.mod`, `go.sum`

**Step 1: Write failing tests**

Cases:
- connect/login/select INBOX/enter IDLE
- on new mail event => emit trigger once
- reconnect on interruption
- stop on context cancel

**Step 2: Run targeted tests (expect fail first)**

```bash
go test ./lib/gmail -run IMAPIdle -v
```

**Step 3: Implement trigger loop**

- `type IMAPIdleTrigger interface { Run(ctx context.Context, out chan<- struct{}) error }`
- Gmail IMAP IDLE implementation (INBOX only)
- duplicate event throttling
- reconnect with bounded retry/backoff

**Step 4: Add IMAP deps**

```bash
go get github.com/emersion/go-imap@latest
go get github.com/emersion/go-imap-idle@latest
```

**Step 5: Re-run tests**

```bash
go test ./lib/gmail -run IMAPIdle -v
```

**Step 6: Commit**

```bash
git add lib/gmail/imap_idle_trigger.go lib/gmail/imap_idle_trigger_test.go go.mod go.sum
git commit -m "feat(imap): add inbox-only idle trigger"
```

---

### Task 4: Add notifications orchestrator (event-triggered history sync)

**Files:**
- Create: `lib/gmail/notifications_sync.go`
- Create: `lib/gmail/notifications_sync_test.go`

**Step 1: Write failing tests**

Cases:
- trigger event causes one `SyncHistoryWithDB` call
- multiple quick events coalesce/debounce
- heartbeat triggers sync without events
- one sync at a time
- sync errors logged; loop continues

**Step 2: Run targeted tests (expect fail first)**

```bash
go test ./lib/gmail -run NotificationsSync -v
```

**Step 3: Implement orchestrator**

Implement `RunNotificationsSyncLoop(...)`:
- consumes trigger channel
- debounce timer
- guarded non-overlapping `SyncHistoryWithDB`
- heartbeat ticker for eventual consistency

**Step 4: Re-run tests**

```bash
go test ./lib/gmail -run NotificationsSync -v
```

**Step 5: Commit**

```bash
git add lib/gmail/notifications_sync.go lib/gmail/notifications_sync_test.go
git commit -m "feat(sync): add imap-notifications sync loop"
```

---

### Task 5: Wire `--imap-notifications` in main runtime

**Files:**
- Modify: `main.go`

**Step 1: Baseline tests**

```bash
go test ./...
```

**Step 2: Implement branching**

- One-shot mode: unchanged (list/materialize/history then exit).
- `--imap-notifications` mode:
  1. run bootstrap list/materialize/history once
  2. start notifications sync loop
  3. start IMAP trigger goroutine feeding trigger channel
  4. graceful shutdown on SIGINT/SIGTERM

**Step 3: Re-run tests**

```bash
go test ./...
```

**Step 4: Commit**

```bash
git add main.go
git commit -m "feat: wire --imap-notifications runtime"
```

---

### Task 6: Documentation and operations guide

**Files:**
- Modify: `README.md`
- Create: `docs/imap-notifications.md`

**Step 1: Document setup**

Include:
- required OAuth scope adjustments for IMAP access (if needed)
- app-password caveat vs OAuth2 XOAUTH2
- INBOX-only trigger semantics
- reconnect expectations

**Step 2: Add run example**

```bash
go run . \
  --directory /home/taras/Mail/mailbox/ \
  --imap-notifications \
  --idle-folder INBOX \
  --idle-debounce-sec 3
```

**Step 3: Re-run tests**

```bash
go test ./...
```

**Step 4: Commit**

```bash
git add README.md docs/imap-notifications.md
git commit -m "docs: add --imap-notifications usage and caveats"
```

---

### Task 7: End-to-end verification checklist

**Files:**
- Modify: `docs/imap-notifications.md`

**Step 1: One-shot regression check**

```bash
go run . --directory /tmp/outtake-maildir
```

**Step 2: Notifications mode check**

```bash
go run . --directory /tmp/outtake-maildir --imap-notifications
```
Expected: process stays alive, IDLE established, incoming INBOX mail triggers immediate sync.

**Step 3: Connection resilience check**

- Interrupt network / force server drop.
- Expected: trigger reconnects automatically; process stays alive.

**Step 4: Final verification**

```bash
go test ./...
```

**Step 5: Commit verification notes**

```bash
git add docs/imap-notifications.md
git commit -m "docs: add imap-notifications e2e checklist"
```
