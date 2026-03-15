# TL;DR Plan: Gmail history replay journal in SQLite (full retention)

## Goal
Provide durable local replay when Gmail history is unavailable (expired `startHistoryId`), without relying on filesystem log files.

## Decision
Use SQLite (not JSONL files) for replay journal storage.

Reasons:
- transactional durability with sync state updates
- indexed replay from `startHistoryId`
- simple gap detection and recovery logic
- lower operational complexity than file rotation/scan

## Retention
- **Full retention** (no pruning/deletion of replay journal rows)
- Keep schema compact to limit DB growth

## Compact journal schema
Create `gmail_history_journal` with compact columns:

- `historyId INTEGER NOT NULL`
- `messageId TEXT NOT NULL`
- `op INTEGER NOT NULL`  (enum)
- `flags INTEGER NOT NULL` (bit flags; e.g. exists-after)
- `labels TEXT` (full normalized label set in compact form; nullable)
- `appliedAtMs INTEGER NOT NULL`

Indexes/keys:
- `PRIMARY KEY (historyId, messageId, op)`
- `INDEX (historyId, messageId)`

Notes:
- Prefer deriving `maildirKey` from `messageId` at replay time (avoid storing redundant path text)
- Store **final post-event state** needed for deterministic replay

## Terminology and naming
Use Gmail terminology in code/CLI:
- `startHistoryId`
- `historyId`
- `nextPageToken`
- `committedHistoryId`

## Sync write path
During normal history sync:
1. apply event to local maildir state
2. append journal row for resulting state
3. advance/commit history cursor

Must ensure ordering and durability so committed cursor never exceeds durably journaled+applied state.

## Fallback behavior on Gmail 404/expired history
When Gmail history fetch fails due to expired `startHistoryId`:
1. attempt local replay from SQLite journal where `historyId >= startHistoryId`
2. verify continuity/gap-free replay range
3. replay/apply in convergence mode (self-heal redownload for missing local files; enforce resulting label state)
4. if replay is complete: continue normal sync from resulting cursor
5. if gap/missing coverage: fallback to existing list/full-sync recovery path

## CLI
Add explicit override:
- `--start-history-id <id>`

Semantics:
- sets the run baseline `startHistoryId` for history fetch/replay decisions
- enables **convergence mode** from that point: for each affected message, ensure local state is complete/correct (message file present and labels correct)
- if message file is missing during replay/apply, self-heal by redownloading message+metadata, then apply label/state logic
- never treat this flag as cursor movement only

## Constraints
- no backfill migration required for this step
- no retention pruning (full replay history retained)
