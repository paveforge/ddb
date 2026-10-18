# ddb

A standalone Go CLI for streaming DynamoDB exports and imports. Supports Linux,
macOS, and Windows on amd64 and arm64.

## Install

Release installation becomes available after the first GitHub release is published.
No release has been published as part of implementation.

Linux/macOS:

```sh
curl -fsSL https://raw.githubusercontent.com/paveforge/go-ddb-cli/main/scripts/install.sh | sh
# or
wget -qO- https://raw.githubusercontent.com/paveforge/go-ddb-cli/main/scripts/install.sh | sh
```

Windows PowerShell:

```powershell
irm https://raw.githubusercontent.com/paveforge/go-ddb-cli/main/scripts/install.ps1 | iex
```

Installers detect the architecture, verify the release SHA-256 checksum, and
install without administrator access. Defaults: `$HOME/.local/bin` on Unix,
`%LOCALAPPDATA%\ddb\bin` on Windows. Add the printed directory to PATH if needed.

To pin a version or set a directory, download the installer and run:

```sh
sh install.sh v0.1.0 "$HOME/.local/bin"
```

```powershell
./install.ps1 -Version v0.1.0 -InstallDir "$env:LOCALAPPDATA\ddb\bin"
```

Alternatively download the matching archive and `checksums.txt` from GitHub
Releases, verify its SHA-256, and extract `ddb`/`ddb.exe` into a directory on PATH.
Checksums detect damaged or mismatched downloads; they are distributed by the
same release channel as binaries.

Build from source with Go 1.24 or newer:

```sh
go build -o ./bin/ddb ./cmd/ddb
./bin/ddb version
```

## Quick start

Configure AWS credentials using your existing environment, shared AWS files, or
workload IAM role. `--profile` selects a named AWS profile; `--region` overrides
region settings. No AWS CLI installation is required.

```sh
# Export a table, preserving every DynamoDB type.
ddb export --table users --output users.jsonl --profile production --region us-east-1

# Compress while streaming (.gz automatically enables gzip).
ddb export --table users --output users.jsonl.gz

# Validate a file and destination without changing AWS resources.
ddb import --table users-dev --input users.jsonl.gz --dry-run

# Replace matching items in an existing table.
ddb import --table users-dev --input users.jsonl.gz --overwrite

# Create the table if missing; otherwise check compatibility and import.
ddb import --table-file docs/examples/table.json --input users.jsonl --overwrite

# Validate everything, delete existing records, then import.
ddb import --table users-dev --input users.jsonl \
  --truncate --confirm-table users-dev

# Read an existing Node.js export.
ddb import --table users-dev --input data.json --format json --overwrite

# Write a JSON summary to stdout; progress stays on stderr.
ddb import --table users-dev --input users.jsonl --dry-run --report -
```

Import requires exactly one of `--table` and `--table-file`. Export uses `--table`.
`--file` is an alias for the command's input/output flag; pass only one path flag.
`--overwrite` acknowledges complete replacement of items with matching keys,
including removal of attributes absent from the input. It is not a merge.

See [formats](docs/formats.md), [table definitions](docs/tables.md),
[operations and recovery](docs/operations.md), and [IAM examples](docs/iam.md).
Run `ddb import --help` or `ddb export --help` for all options.

## Large transfers

Export processes one scan page at a time. Import streams the input into a
validated, canonical temporary file before any AWS mutations, then processes it
with bounded batches and workers. Memory depends on page size and concurrency,
not total record count. Temporary disk space is required; see the operations guide.

```sh
ddb export --table users --output users.jsonl.gz --page-size 100 --read-rate 5
ddb import --table users-dev --input users.jsonl.gz --overwrite \
  --concurrency 4 --write-rate 10 --retry-attempts 8 --retry-timeout 2m
```

Rates are **requests per second**, not capacity-unit budgets. Item sizes and
indexes affect consumed capacity; monitor the destination and adjust pacing.

Exports are live scans, not point-in-time snapshots. Imports and truncation are
not atomic. Quiesce other writers when a stable copy or empty destination is
required. Checkpoint resume and managed S3 transfers are planned follow-ups.

## Development

```sh
go test -race ./...
go vet ./...
```

Tests ordinarily require no AWS account. The integration suite runs against
DynamoDB Local when `DDB_TEST_ENDPOINT` is set; details are in
[CONTRIBUTING.md](CONTRIBUTING.md). GitHub Actions runs native tests across all
three operating systems, local integration tests, and six release builds.

License: [MIT](LICENSE).
