# DynamoDB CLI implementation plan

Status: implementation approved and milestones implemented on `main`; see progress below.

## 1. Purpose

Build an open-source collection of AWS DynamoDB tools in Go. The first release focuses on reliable import and export, including large datasets, optional table creation, and optional deletion of existing data before import.

Ship a standalone binary named `ddb` that requires neither Go nor Node.js on the target machine. Support local use, CI, and scripted operations on Linux, macOS, and Windows.

This document records the agreed direction. Creating it does not authorize implementation, publishing, or changing repository visibility.

## 2. Reference implementation

Reference: the private Node.js project `dotronglong/aws-dynamodb-data-transfer`, reviewed locally at `/Users/lucas/Works/github/aws-dynamodb-data-transfer`.

Retain these useful workflows:

- Export one table to a file.
- Import a file into one table.
- Optionally delete existing items before importing.
- Discover partition and sort keys automatically.

Improve on the reference implementation:

- Stream data rather than accumulating the complete dataset in memory.
- Retry unprocessed writes and deletes instead of only logging them.
- Return unsuccessful exit codes for failures.
- Validate input before destructive operations.
- Preserve DynamoDB types through an explicit lossless format.
- Provide native binaries, automated tests, release automation, and installers.

Implement the Go project independently. Do not copy private dependencies, credentials, configuration, or business data into the open-source repository.

## 3. MVP scope

### Included

- Export an existing table through a paginated scan.
- Import into an existing table.
- Create a missing destination table from a JSON definition.
- Optionally truncate the destination before importing.
- DynamoDB JSON Lines and explicit plain JSON array compatibility.
- Streaming gzip compression and decompression.
- Full import preflight validation and dry run.
- Bounded concurrency, queues, retries, and request pacing.
- Progress, machine-readable reports, and import recovery artifacts.
- GitHub Actions testing and tagged releases.
- Cross-platform binaries and one-command installers.
- Public documentation and proposed MIT licensing.

### Deferred

- Resumable checkpoints and chunked transfer files.
- Parallel scans.
- Direct table-to-table copying.
- Managed DynamoDB export/import through S3.
- CSV, Amazon Ion, and arbitrary transformations.
- Continuous replication and DynamoDB Streams.
- Table schema migrations and automatic modification of existing tables.
- TTL, autoscaling, and other configuration outside `CreateTable`.
- Interactive UI and a plugin framework.

## 4. CLI contract

### Export

```sh
ddb export --table users --output users.jsonl --profile production
ddb export --table users --output users.jsonl.gz --compression gzip
ddb export --table users --output users.json --format json
```

- Require `--table` and an output path.
- Default format: `dynamodb-jsonl`.
- Support `--format json` for a plain JSON array, with documented type limitations.
- Support `--file` as an alias for `--output`; reject conflicting path arguments.
- Refuse to replace an existing output unless `--force` is supplied.
- Expose page size, read pacing, and optional strongly consistent reads.
- Scan the base table sequentially in the MVP.

### Import

```sh
ddb import --table users-dev --input users.jsonl --overwrite
ddb import --table-file ./tables/users.json --input users.jsonl --overwrite
ddb import --table-file ./tables/users.json --input users.jsonl --dry-run
ddb import --table users-dev --input users.jsonl \
  --truncate --confirm-table users-dev
```

- Require exactly one of `--table` or `--table-file`.
- Require an input path; support `--file` as an alias for `--input`.
- With `--table`, require the destination to exist.
- With `--table-file`, get the destination name from the definition and create the table only if missing.
- Ordinary imports require `--overwrite` acknowledgement because matching keys replace complete items. Confirmed truncation supplies its own acknowledgement. Dry run does not require a write acknowledgement.
- Support configurable write concurrency, pacing, and retry limits.
- No automatic skipping of invalid records.

### Shared behavior

- `--profile`: select a standard named AWS profile.
- `--region`: override region configuration.
- `--endpoint-url`: support DynamoDB Local and compatible development environments.
- Otherwise follow the AWS SDK's standard configuration and credential discovery.
- Never require credentials directly in command arguments.
- Provide `ddb version` and normal command help.
- Keep progress and diagnostics on stderr; offer a JSON report for automation.
- Avoid interactive prompts. Use explicit flags for acknowledgements.

Exact names for tuning and report flags will be finalized during CLI implementation and documented consistently.

## 5. Data formats

### Lossless default: DynamoDB JSON Lines

Use one `Item` wrapper per line, matching the shape of AWS full-export DynamoDB JSON data records:

```json
{"Item":{"id":{"S":"user-1"},"balance":{"N":"123.45"}}}
```

- Preserve numbers as decimal strings without floating-point conversion.
- Preserve binary values, sets, maps, lists, booleans, nulls, and strings.
- Validate attribute-value tags and their values recursively.
- Do not claim support for complete S3 manifests or incremental-export records.
- Document the format and compatibility policy before implementation.

### Plain JSON arrays

- Support explicit `--format json` for compatibility with Node.js exports.
- Use a streaming JSON decoder rather than loading the array.
- Map objects to maps, arrays to lists, numbers to DynamoDB numbers, and normal JSON scalars to their corresponding types.
- Preserve numeric text during parsing.
- Do not guess that strings are binary or arrays are sets.
- Document that old plain JSON exports may already have lost type information.
- Plain JSON export must reject values it cannot represent faithfully or require an explicit documented lossy conversion option; never silently discard type information.

### Compression and paths

- Support explicit `none` and `gzip` compression modes.
- Define and document predictable `.gz` inference, with explicit flags taking precedence.
- Treat malformed or truncated gzip input as an error during preflight.
- MVP inputs are seekable local files. stdin, remote URLs, and pipelines are deferred because full preflight and reliable recovery require rereading input.

## 6. Table definitions and lifecycle

The JSON file uses the DynamoDB `CreateTable` request structure. `TableName` is mandatory.

```json
{
  "TableName": "users-dev",
  "BillingMode": "PAY_PER_REQUEST",
  "AttributeDefinitions": [
    { "AttributeName": "id", "AttributeType": "S" }
  ],
  "KeySchema": [
    { "AttributeName": "id", "KeyType": "HASH" }
  ]
}
```

Support applicable creation properties such as primary keys, local/global secondary indexes, billing mode, throughput, and other SDK-supported `CreateTable` properties. Reject unknown fields and locally detectable invalid combinations. AWS remains authoritative for service-side constraints.

Execution order:

1. Parse flags, definition, and input configuration.
2. Read AWS/table state and validate the complete input without mutation.
3. If required, create the missing table.
4. Wait for the table and relevant indexes to be ready, with a bounded timeout.
5. Revalidate actual destination schema before writes.
6. If requested, complete truncation.
7. Import records.

Existing-table compatibility:

- Compare primary key names and types.
- For a supplied definition, compare local/global index key schemas and projections; report incompatible definitions rather than changing them.
- Validate item attributes used as table or index keys.
- Do not require operational properties such as current provisioned throughput to match the creation file.
- Do not automatically reconcile billing settings, tags, encryption, or other configuration.
- If another actor creates the table between inspection and creation, re-describe it and apply compatibility checks.

If creation succeeds but a later phase fails, keep the table and report that it was created. Never automatically delete it as rollback.

## 7. Preflight and dry run

Full validation is the default for imports, using a streaming first pass followed by a second pass for execution.

Preflight checks:

- Required and mutually exclusive flags.
- Input readability and output/report/recovery path validity.
- File syntax, complete JSON consumption, compression integrity, and record shape.
- Attribute types, primary keys, applicable index keys, item size, and request feasibility.
- Table definition validity and existing-table compatibility.
- AWS credentials, connectivity, and destination state through necessary read calls.
- Required acknowledgements before mutation.

Preflight cannot prove future write permissions or guarantee later success. Permissions, capacity, connectivity, and table state may change during execution.

Prevent input changes between passes by using a stable file handle, metadata checks, and a content fingerprint. Before truncation, stage a validated local copy if needed to guarantee that execution uses the validated bytes. Account for staging disk requirements and clean up temporary files.

Dry run performs read-only validation, reports intended creation/truncation/write actions, and makes no table or item changes.

## 8. Large-data architecture

```text
Export: scan page -> bounded buffer -> encoder -> optional gzip -> partial file
Import: file -> optional gunzip -> decoder -> bounded queue -> batch writers
```

- Memory is bounded by configured page, queue, batch, and worker limits rather than dataset size.
- Backpressure pauses reads when downstream work slows.
- Validate record sizes with bounded parsing safeguards while supporting valid near-limit DynamoDB items.
- Provide conservative defaults and document how concurrency and pacing affect table load.
- Track scanned/exported records, validated/confirmed records, bytes, elapsed time, retries, and returned consumed capacity.
- Do not show a misleading percentage when the total is unknown.

Export files:

- Write to a sibling `.partial` path.
- Close and check encoder, gzip, and file errors before declaring completion.
- Rename to the final path only on success, using platform-appropriate replacement behavior.
- Protect both final and partial paths against accidental replacement.
- Preserve identifiable partial output after interruption; do not imply it is a complete dataset.

Scan exports are live views, even with strongly consistent reads. They do not provide a table-wide snapshot.

## 9. Import batching and duplicate keys

- Respect DynamoDB's maximum 25 operations per batch, 16 MB request limit, and 400 KB stored item limit.
- Calculate stored item size separately from JSON wire size.
- Never place duplicate primary keys in the same batch.
- Serialize writes for matching keys across concurrent batches so source order has predictable replacement behavior.
- Use bounded key-aware scheduling rather than an unbounded set of every key in the file.
- Repeated source keys are processed in source order; the last successfully processed record wins when the full import completes.
- Retry only unprocessed members of a successful batch response.
- Coordinate SDK request retries and application retries to avoid excessive combined retry durations.

## 10. Truncation

- Require `--truncate --confirm-table <resolved-table-name>`.
- Fully validate and stabilize input before deleting anything.
- Discover key names/types and scan only key attributes.
- Delete in bounded batches with retry handling.
- Complete deletion before scheduling import writes.
- On deletion failure, do not start importing.
- Never drop and recreate the table as an implementation of truncation.

Truncation is not atomic. Concurrent application writes can leave or introduce records, and failures can leave a partially emptied table. Document that callers requiring a clean replacement must quiesce other writers. No automatic rollback is promised.

## 11. Errors during execution

| Failure | Required behavior |
| --- | --- |
| Throttling or transient service/network error | Retry within bounded attempt and elapsed-time budgets, with exponential backoff and jitter |
| Unprocessed batch items | Retry only unprocessed operations |
| Invalid item, incompatible schema, permission denial | Stop scheduling new operations and report the cause |
| Table creation failure/readiness timeout | Stop before import; report known creation state |
| Export disk/encoding/write failure | Stop scanning and preserve partial output |
| User cancellation | Stop scheduling, settle in-flight work within a timeout, and report partial completion |
| Recovery/report write failure | Report it explicitly without masking the original error |

Fail fast by default. Already-running operations may still complete after a failure is detected. Do not silently skip items, retry permanent failures indefinitely, or report success while work remains unresolved.

Use cancellable contexts throughout. A second interrupt may force immediate termination; document that forced termination can prevent complete recovery artifacts.

## 12. Recovery and reporting

Every command failure returns a nonzero exit code and identifies the failed phase: preflight, creation, truncation, export, import, or finalization.

Report:

- Resolved table, region, and endpoint where applicable.
- Confirmed successes.
- Attempted but unresolved records.
- Records not attempted.
- Retry counts and elapsed time.
- Whether a table was created or truncation began/completed.
- Locations and completeness of partial files and recovery artifacts.

Do not log credentials or item bodies. Recovery files necessarily contain data and should use restrictive permissions where supported.

For failed imports:

- Produce a replayable recovery file containing every source record without a confirmed successful write, including records not attempted.
- Prefer the canonical lossless format and preserve source ordering for repeated keys.
- Use the stable input to stream remaining records into recovery without loading them into memory.
- Validate the recovery destination before mutation, but acknowledge that disk failures can still occur later.
- If complete recovery output cannot be produced, mark it incomplete and preserve the original input for restart.
- State when outcomes are uncertain because a response was lost after a request may have committed.

Replays provide at-least-once writes. They may repeat a write and can overwrite concurrent application changes. This is not checkpoint resume or exactly-once delivery.

For failed truncation, report the deletion phase and counts; do not create an import recovery file that implies import had started. Retrying truncation may be necessary before rerunning the import.

For failed exports, keep `.partial` output. Restart export in the MVP; reliable cursor-based resume is deferred.

Define documented exit-code categories for usage/validation errors, AWS/transfer failures, local I/O failures, and cancellation. JSON reports expose stable fields and a report schema version.

## 13. Go design

Use AWS SDK for Go v2 and Cobra. Prefer standard libraries for JSON, gzip, structured logging, filesystem access, and cancellation. Select supported dependency versions when implementation begins and pin them in module files.

```text
cmd/ddb/                 Entry point
internal/cli/            Commands, flags, exit-code mapping
internal/awsconfig/      AWS configuration and clients
internal/table/          Definitions, compatibility, creation, waiters
internal/transfer/       Export, import, truncation, batching, retries
internal/format/         Streaming codecs and validation
internal/report/         Progress, summaries, recovery metadata
scripts/                 Shell and PowerShell installers
.github/workflows/       Tests and releases
```

Use narrow service interfaces to simulate pagination, partial responses, throttling, cancellation, and ambiguous outcomes. Avoid building abstractions solely for hypothetical future features.

## 14. Testing and CI

GitHub Actions on pull requests and pushes:

- Formatting checks, `go vet`, unit tests, and race detection where supported.
- Native CLI tests on Linux, macOS, and Windows.
- DynamoDB Local integration tests on Linux.
- Cross-compilation of every release target.
- Installer tests using local release fixtures.

Meaningful test scenarios:

- Round trips for every DynamoDB value type and large numeric values.
- Empty files/tables, malformed/trailing JSON, invalid gzip, and near-limit items.
- JSON array compatibility and explicit lossy-format rejection.
- Pagination and bounded memory on large generated datasets.
- Exclusive table flags, invalid definitions, table creation races, and readiness timeouts.
- Existing table/index compatibility and dry-run non-mutation.
- Truncation validation order, key-only scans, partial delete failures, and suppression of import after deletion failure.
- Batch size limits, repeated keys, partial responses, retry exhaustion, and permanent failures.
- Cancellation and ambiguous request outcomes.
- Disk failures, partial output, recovery completeness, and exit/report accuracy.

Use mocks for throttling and service behavior that DynamoDB Local cannot reproduce. Do not require live AWS credentials for ordinary CI. Any later live-AWS smoke tests are opt-in and use isolated resources.

## 15. Releases

Use GitHub Actions and GoReleaser on version tags such as `v0.1.0`.

Publish:

- Linux amd64 and arm64 binaries.
- macOS amd64 and arm64 binaries.
- Windows amd64 and arm64 binaries.
- Appropriate tar.gz/zip archives.
- SHA-256 checksum manifest.
- Release notes and embedded version/commit/build metadata.

Keep CI permissions minimal and grant release-write permissions only to the release job. Pin workflow dependencies appropriately. Verify release configuration before publication.

## 16. Installation

Provide `install.sh` for Linux/macOS and `install.ps1` for Windows.

Illustrative commands; replace placeholders with the chosen published installer URLs:

```sh
curl -fsSL <installer-url>/install.sh | sh
wget -qO- <installer-url>/install.sh | sh
```

```powershell
irm <installer-url>/install.ps1 | iex
```

Both installers must:

- Detect OS and architecture, including common architecture aliases.
- Download the matching versioned release asset over HTTPS.
- Verify its SHA-256 checksum before installing.
- Support latest stable and explicitly pinned versions.
- Support a custom installation directory.
- Default to a user-writable location and explain PATH setup.
- Handle paths containing spaces, cleanup, upgrades, and existing installations.
- Fail clearly on unsupported platforms without installing the wrong binary.
- Require neither Go nor Node.js and avoid automatic privilege escalation.

Also document manual archive installation. macOS signing/notarization and package-manager distribution are deferred unless needed for the agreed distribution experience.

## 17. Open-source documentation

Proposed license: MIT, to be confirmed before publication.

Include:

- README with installation, quick start, supported platforms, and command examples.
- Format specification and plain JSON limitations.
- Table-definition examples and compatibility rules.
- Large-transfer tuning and consistency limitations.
- Error, recovery, and cancellation behavior.
- Least-privilege IAM examples for export, import, optional creation, and optional truncation.
- Contribution guide, security reporting instructions, and issue templates.

Changing repository visibility, publishing releases, and distributing installers are separate external actions requiring authorization. This plan does not make the repository public.

## 18. Delivery milestones

### Milestone 1: foundation

- Confirm format and CLI contracts.
- Initialize Go module, command structure, configuration, reporting, and initial CI.
- Define narrow AWS interfaces and common validation/error types.

### Milestone 2: export

- Implement sequential paginated scans, streaming codecs, gzip, pacing, and partial-file handling.
- Verify type preservation, cancellation, I/O errors, and bounded memory.

### Milestone 3: import and table lifecycle

- Implement full streaming preflight and stable input handling.
- Add table-file parsing, compatibility checks, creation, and readiness waiting.
- Implement batching, concurrency, duplicate-key ordering, retries, dry run, and recovery artifacts.
- Add confirmed truncation with key-only scans and deletion retries.
- Verify partial failures and accurate outcomes end to end.

### Milestone 4: distribution and documentation

- Add shell and PowerShell installers and fixture-based tests.
- Add GoReleaser and release workflows.
- Finish documentation, IAM examples, and open-source project files.
- Verify supported binaries and installer behavior before the first authorized release.

## 19. MVP acceptance criteria

- Supported DynamoDB values survive a lossless export/import round trip.
- Large transfers use bounded memory with backpressure.
- `--table` and `--table-file` are mutually exclusive and behave as documented.
- Missing tables are created only when a valid table file is supplied.
- Dry run makes no mutations.
- Input validation completes before truncation or writes.
- Retriable failures recover within bounded budgets; unresolved work never produces success.
- Failure reports distinguish confirmed, uncertain, and unattempted records.
- Recovery artifacts are replayable or explicitly marked incomplete.
- Export final paths represent completed files only.
- CI passes and supported release binaries/installers are verified.
- Documentation explains overwrite, truncation, snapshot, and recovery limitations.

## 20. Next milestones after MVP

1. Checkpoints and independently compressed chunks for reliable, efficient resume.
2. Parallel scans with per-segment checkpoints and capacity controls.
3. Direct copying and optional transformations.
4. Managed S3 workflows for very large production transfers and point-in-time exports.

Checkpoint designs must bind progress to input/output identity and target settings, commit only durable progress, and explicitly retain at-least-once semantics where outcomes are uncertain.

## 21. Implementation progress

- Milestone 1 committed: `19e786a` — CLI foundation, AWS configuration, reports, initial CI.
- Milestone 2 committed: `1a5899e` — streaming export, codecs, gzip, partial-file handling.
- Milestone 3 committed: `bf2eb93` — import preflight, table lifecycle, truncation, retries, recovery.
- Milestone 4 complete: distribution, documentation, pinned workflows, installer fixtures, and final hardening; committed with this progress update.

Verification performed locally:

- Go unit and race tests, vet, formatting, and shell installer fixtures.
- DynamoDB Local integration covering creation, all value types, paginated gzip export, truncation, and reimport.
- GoReleaser configuration validation and snapshot archives for all six targets, archive layout/checksums, and native release-binary CLI smoke tests.
- Large import resource test: source exceeds 117 MiB; sampled heap growth approximately 3 MiB on the development machine.

Native Windows/PowerShell and Linux/macOS checks are configured in GitHub Actions;
Windows execution has not been performed locally. No release was published, no
changes were pushed, and repository visibility was not changed.

Implementation details clarified during development:

- All imports stage canonical validated input, trading temporary disk space for a stable source and bounded-memory recovery.
- Initial stored-number size checks use a documented conservative bound, which can reject some otherwise valid near-limit items.
- Duplicate-key scheduling uses bounded waves; wire-size partitioning can split a wave into additional batches while retaining the configured worker limit.
- Export publication without force uses a no-clobber hard link; unsupported filesystems fail safely and retain the partial file.
