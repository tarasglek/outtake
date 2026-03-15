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
