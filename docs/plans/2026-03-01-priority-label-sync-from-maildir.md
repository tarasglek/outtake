# Priority: Label Sync Source-of-Truth Migration (Maildir Headers)

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Fix label-sync correctness by removing SQLite `gmail_message_labels` as label state authority and using each message’s maildir `X-Keywords` header as the source of truth for label delta application.

**Architecture:** History label events will read current labels from the mail file, apply add/remove delta, and rewrite headers. If the local mail file is missing for a label event, self-heal by fetching the message+metadata from Gmail API and writing it locally, then apply labels. SQLite continues to store history cursor/progress only.

**Tech Stack:** Go, existing Gmail API sync pipeline, maildir storage, SQLite for cursors/checkpoints.

---

### Task 1: Add failing tests that capture current label-sync gaps

**Files:**
- Modify: `lib/gmail/history_sync_test.go`
- Create: `lib/gmail/label_state_from_maildir_test.go`

**Step 1: Add failing test: label delta works without DB label rows**

Scenario:
- message exists in maildir with `X-Keywords: One`
- no `gmail_message_labels` row exists
- history label add event arrives for label `Two`
- expected: file now contains `X-Keywords: One` and `X-Keywords: Two`

**Step 2: Add failing test: missing file triggers self-heal**

Scenario:
- history label event arrives for unknown/missing local file
- mock service returns raw+metadata
- expected: mail file created and labels applied

**Step 3: Run targeted tests to verify failure**

```bash
go test ./lib/gmail -run "LabelStateFromMaildir|HistoryLabel" -v
```
Expected: FAIL.

**Step 4: Commit tests**

```bash
git add lib/gmail/history_sync_test.go lib/gmail/label_state_from_maildir_test.go
git commit -m "test(label-sync): add failing tests for maildir-based label state"
```

---

### Task 2: Implement label-state reader from maildir headers

**Files:**
- Create: `lib/gmail/maildir_labels.go`
- Create: `lib/gmail/maildir_labels_test.go`

**Step 1: Implement parser + normalizer**

Add helper(s):
- `readLabelsFromMaildirMessage(...) ([]string, error)`
- normalize ordering/deduplicate for deterministic writes

Use `X-Keywords` header as authoritative label names.

**Step 2: Add tests for parser edge-cases**

Cases:
- no header => empty labels
- multiple keyword headers
- duplicate labels
- whitespace handling

**Step 3: Run targeted tests**

```bash
go test ./lib/gmail -run MaildirLabels -v
```
Expected: PASS.

**Step 4: Commit**

```bash
git add lib/gmail/maildir_labels.go lib/gmail/maildir_labels_test.go
git commit -m "feat(label-sync): read label state from maildir headers"
```

---

### Task 3: Rework history label delta path to use maildir state

**Files:**
- Modify: `lib/gmail/history_sync.go`

**Step 1: Replace DB label-state reads in `applyHistoryLabelDelta`**

New flow:
1. Resolve file key `messageMaildirKey(id)`
2. Try load maildir message
3. If present: read labels from `X-Keywords`
4. Apply add/remove delta in-memory
5. Rewrite headers and persist message

**Step 2: Implement missing-file self-heal (chosen behavior)**

If file missing:
- fetch message via existing `handleNewMsg` path (or equivalent raw+metadata)
- write message with mapped labels
- then apply delta and persist

**Step 3: Keep operation idempotent**

Ensure repeated same delta produces stable header state.

**Step 4: Re-run targeted tests**

```bash
go test ./lib/gmail -run "LabelStateFromMaildir|HistoryLabel" -v
```
Expected: PASS.

**Step 5: Commit**

```bash
git add lib/gmail/history_sync.go
git commit -m "fix(label-sync): apply history deltas from maildir label state with self-heal"
```

---

### Task 4: Remove `gmail_message_labels` usage from runtime code

**Files:**
- Modify: `lib/gmail/history_sync.go`
- Modify: `lib/gmail/message_sync.go`
- Modify: any `labels_sql.go` call sites

**Step 1: Remove writes/reads tied to `gmail_message_labels` for runtime delta logic**

- eliminate `getMessageLabels`, `replaceMessageLabels`, `applyLabelDelta` dependencies in history path
- keep label name mapping table (`gmail_labels`) if still needed for ID->name mapping

**Step 2: Ensure no runtime dependency remains**

```bash
rg -n "getMessageLabels|replaceMessageLabels|applyLabelDelta|gmail_message_labels" lib/gmail
```
Expected: only legacy helper definitions or migration comments, no active history path usage.

**Step 3: Run full tests**

```bash
go test ./...
```
Expected: PASS.

**Step 4: Commit**

```bash
git add lib/gmail/history_sync.go lib/gmail/message_sync.go lib/gmail/labels_sql.go
git commit -m "refactor(label-sync): remove sqlite message-label state from active sync path"
```

---

### Task 5: Schema and migration cleanup (safe, non-breaking)

**Files:**
- Modify: `lib/gmail/list_pages_schema.go`
- Create: `docs/label-sync-migration.md`

**Step 1: Mark `gmail_message_labels` deprecated (compat period)**

Option A (safer): keep table creation but unused.
Option B (later migration): drop table with explicit migration step.

For this priority fix, choose **Option A** to avoid risky DB migrations now.

**Step 2: Document cleanup strategy**

Add follow-up note for eventual table removal and one-time maintenance script.

**Step 3: Commit**

```bash
git add lib/gmail/list_pages_schema.go docs/label-sync-migration.md
git commit -m "docs(schema): deprecate gmail_message_labels for future cleanup"
```

---

### Task 6: Observability updates for label convergence

**Files:**
- Modify: `lib/gmail/history_sync.go`

**Step 1: Replace `labels_missing_state` metric semantics**

New counters:
- `labels_missing_file` (existing)
- `labels_self_healed` (new; missing file recovered by download)
- `labels_apply_failed` (explicit failures)

**Step 2: Update log messages**

Clarify that missing local file triggers self-heal instead of skip.

**Step 3: Verify by test/log assertions**

```bash
go test ./lib/gmail -run History -v
```
Expected: PASS.

**Step 4: Commit**

```bash
git add lib/gmail/history_sync.go
git commit -m "chore(observability): update history label sync counters for self-heal flow"
```

---

### Task 7: End-to-end verification checklist (must run before completion)

**Files:**
- Modify: `docs/label-sync-migration.md`

**Step 1: Regression run**

```bash
go test ./...
```

**Step 2: Real mailbox sanity run**

```bash
go run . --directory /path/to/maildir
```
Then trigger label add/remove on known messages and verify file headers changed.

**Step 3: Missing-file self-heal test**

- delete one local mail file manually (keep message on Gmail)
- trigger label change on Gmail
- rerun sync
- expected: file recreated with correct labels

**Step 4: Final verification**

Capture final logs and ensure no large `labels_missing_state` remains (counter removed/replaced).

**Step 5: Commit final docs**

```bash
git add docs/label-sync-migration.md
git commit -m "docs: add e2e verification for maildir-based label sync"
```
