# Label Sync Migration: SQLite message labels -> Maildir X-Keywords

## Summary

Label delta application now uses each message file's `X-Keywords` header as the source of truth.
The SQLite table `gmail_message_labels` is removed from active schema creation and is no longer used by sync flows.

## Runtime behavior

- History label add/remove events load current label state from maildir headers.
- Deltas are applied in-memory and persisted back to `X-Keywords`.
- If a message file is missing locally during label delta handling, sync attempts a self-heal:
  1. fetch message body + metadata from Gmail,
  2. write it to maildir,
  3. then apply the label delta.

## Schema compatibility plan

`gmail_message_labels` is no longer created by `ensureListPagesSchema`.

- Existing databases that already contain `gmail_message_labels` continue to work; the table is simply unused.
- New databases will not create this table.
- Optional maintenance (manual, after local validation):

```sql
DROP TABLE IF EXISTS gmail_message_labels;
```

## Observability counters

History sync now tracks:

- `labels_missing_file`: label delta encountered a missing local message file
- `labels_self_healed`: missing file was recovered and label delta applied
- `labels_apply_failed`: explicit label-delta processing failures

## Debugging notes (future incidents)

Use this quick checklist when labels look stale or wrong locally.

1. **Check history counters in logs**
   - healthy path should show `labels_applied` increasing
   - if files are missing, expect `labels_missing_file` and `labels_self_healed` to increase together
   - repeated growth in `labels_apply_failed` indicates a hard failure path

2. **Inspect one affected message on disk**
   - locate key: `<gmailMessageId>.mail`
   - verify `X-Keywords` header contains expected label names (not label IDs)

3. **Confirm label ID -> name mapping health**
   - run a sync that refreshes labels if unknown IDs appear
   - unknown labels can temporarily appear as raw IDs until label metadata is refreshed

4. **Self-heal expectations**
   - when file is missing during history replay, log should include:
     - `missing local file for label delta ... attempting self-heal`
   - if self-heal succeeds, message file should be recreated before delta rewrite

5. **Idempotency sanity check**
   - replaying the same label add/remove event should not duplicate labels
   - headers should remain normalized/deduplicated/sorted

6. **Legacy table note**
   - `gmail_message_labels` is not used by runtime sync
   - do not debug label correctness from that table

## End-to-end verification checklist

### Automated regression

Run:

```bash
go test ./...
```

### Real mailbox sanity run

Run:

```bash
go run . --directory /path/to/maildir
```

Then in Gmail, add/remove labels on known messages and verify local files update `X-Keywords` accordingly.

### Missing-file self-heal validation

1. Delete one local mail file manually (keep message on Gmail).
2. Trigger a label change for that message in Gmail.
3. Re-run sync.
4. Confirm the file is recreated and contains correct `X-Keywords` labels.

### Final log check

Verify history logs report `labels_missing_file`, `labels_self_healed`, and `labels_apply_failed`, and no longer use `labels_missing_state`.
