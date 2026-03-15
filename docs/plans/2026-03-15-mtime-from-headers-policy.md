# Plan: Apply mail file mtime from message headers (strict RFC parsing)

## Scope
Apply mtime when outtake writes message files during normal sync flows.

- Included: initial materialization/write path, history add/self-heal write path
- Excluded: any backfill/retime command for existing files

## Policy
Implement header timestamp selection with this order:

1. Topmost `Received:` header (primary)
2. `Date:` header (fallback)
3. If neither is parseable/present, do not override mtime

### Required code comment near implementation
Document rationale directly above the selector/parsing function:

- `Received` best represents delivery timing into mailbox pipeline.
- Outbound local copies (commonly SENT/DRAFT) often lack `Received`.
- Therefore fallback to `Date` is required to avoid skipping legitimate messages.

## Parsing constraints
Use RFC parsing only (no heuristic normalization).

- Prefer Go standard parsing utilities/layouts for RFC mail dates.
- If header value is not RFC-parseable, treat as unavailable and continue.

## Integration points
After any successful message write/rewrite operation:

1. compute timestamp using policy above
2. if available, set file mtime
3. if unavailable, keep default filesystem mtime

Must be applied consistently to all paths that can rewrite `.mail` files, including:

- initial materialization/write path
- history add/self-heal write path
- label-update rewrite path (header rewrite after label delta)

## Error handling / logging
mtime logic must never fail the sync operation.

- If timestamp cannot be determined from headers, log warning with filepath and clear reason:
  - `mtime: cannot determine from headers file=<path> (no parseable Received/Date)`
- If setting mtime fails, log warning with filepath and OS error.

## Tests
Add/adjust tests for mtime selection/parsing behavior:

1. topmost `Received` wins when multiple exist
2. fallback to `Date` when `Received` missing
3. missing/invalid both -> no timestamp returned
4. successful write path applies expected mtime when timestamp exists
