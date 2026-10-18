# Operations, errors, and recovery

## Preflight

Import validates flags, destination state, the table definition when supplied,
all records, keys, attribute types, number precision/range, and conservative item
size before mutations. It stages canonical JSON Lines in a restrictive temporary
file so changing the original file cannot change the execution data. A SHA-256
fingerprint of the decompressed source is included in reports.

Input must be a local regular file. Format limits bound each encoded record to
4 MiB and DynamoDB document nesting to 32 levels. Stored-size validation uses a
conservative 21-byte estimate per number, so items extremely close to 400 KiB
with many small numbers can be rejected. AWS remains authoritative for sizes and
service validation. Plain JSON arrays are parsed one bounded object at a time.

The temporary directory (standard OS temp directory; configurable with the Go
runtime's normal `TMPDIR`/`TMP` environment) needs space for an uncompressed,
canonical copy of input plus one confirmation byte per record. The recovery
location needs room for the unresolved and unattempted records. Temporary files
are removed on ordinary success/failure; forced termination may leave `ddb-*`
temporary files to remove manually. Input and recovery files contain application
data; protect them appropriately.

A successful dry run validates and reports without AWS mutations. It does create
and remove local temporary files and can write an explicitly requested report.
Input validation is followed by a second check of destination schema/readiness.

## Runtime behavior

- Retry throttling, transient service/network failures, and unprocessed batch
  members. Default: 8 attempts and 2 minutes per request/batch, including pacing.
- SDK retries are disabled so there is one retry budget. HTTP requests have a
  30-second timeout. Creation readiness has its own bounded wait.
- Permanent failures stop new scheduling. In-flight operations are cancelled and
  awaited; some may already have committed.
- Duplicate primary keys are never sent together. Matching keys are serialized
  across workers, with later input values replacing earlier values on success.
- No record is silently skipped. Failure or unresolved work returns nonzero.
- Ctrl-C or SIGTERM cancels work; a second interrupt forces termination and may prevent a
  complete report/recovery file.

Progress goes to stderr at most once per second, plus a final summary. JSON
reports (`--report path` or `--report -`) have `schema_version: 1`. Report paths
must be new files. Logs/reports do not intentionally include item bodies or
credentials. Export byte counts are physical output bytes; import byte counts
are canonical staging bytes. AWS-returned consumed capacity is accumulated.

Exit codes:

| Code | Meaning |
| --- | --- |
| 0 | Completed successfully, including successful dry run |
| 2 | Flags, configuration, schema, or input validation failure |
| 3 | AWS or transfer failure |
| 4 | Local filesystem/report failure |
| 130 | Cancellation |

## Export failure

Output is written to `OUTPUT.partial`, exclusively created. An existing partial
file is never replaced automatically. The final path is published only after
encoding, compression, flush, sync, and close succeed. Without `--force`, output
publication uses a no-clobber hard link and requires a filesystem supporting hard
links; otherwise the command fails and preserves the partial file. With `--force`,
a rename replaces the completed output using native filesystem behavior.

Interrupted output is incomplete; compressed output may be unreadable. Keep it
for inspection or remove it explicitly and restart. Cursor resume is not in the
MVP. Strongly consistent scans still do not create a table-wide snapshot.

## Import failure

Before mutation, reserve `INPUT.recovery.jsonl` (or `--recovery path`); refuse to
replace an existing file. On success the reservation is removed. If the import
phase fails, emit every record without a confirmed successful write, in original
source order, including records not attempted. Use canonical DynamoDB JSON Lines
regardless of the original format/compression. Temporary confirmation state stays
on disk, keeping memory bounded.

```sh
ddb import --table users-dev --input users.jsonl.recovery.jsonl --overwrite
```

Check `recovery_complete` in the report. If disk failure, forced termination, or
another local problem prevents complete output, keep the original input and do
not assume the recovery file contains everything. Retry with a fresh recovery
path if a previous artifact already exists.

Reports also expose `would_create_table`, `would_truncate`, creation attempts and
uncertainty, and unresolved deletes. Dry runs therefore describe the proposed
mutations explicitly.

Reports distinguish confirmed writes, unresolved attempts, and records not
attempted. Lost responses can leave writes uncertain. Replays are at least once,
can repeat writes, and can overwrite concurrent application changes. They do not
provide rollback or exactly-once delivery.

Creation failure leaves no import recovery file because importing has not begun.
A table that was created is retained. Inspect its state before retrying.

## Truncation failure

`--truncate --confirm-table NAME` validates everything before deleting, then
scans only primary keys and deletes in batches. It never drops the table.
Import starts only after all deletes finish. A deletion failure can leave a
partially emptied table; no import recovery file is produced. Inspect the report
and rerun confirmed truncation when appropriate.

Truncation is not atomic and does not prevent concurrent writes. Stop other
writers to guarantee an empty boundary. There is no automatic rollback.
