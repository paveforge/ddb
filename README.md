# ddb

A standalone Go CLI for streaming DynamoDB exports and imports. Supports Linux,
macOS, and Windows on amd64 and arm64.

## Install

Release installation becomes available after the first GitHub release is published.
No release has been published as part of implementation.

Linux/macOS:

```sh
curl -fsSL https://raw.githubusercontent.com/paveforge/ddb/main/scripts/install.sh | sh
```

Or with wget:

```sh
wget -qO- https://raw.githubusercontent.com/paveforge/ddb/main/scripts/install.sh | sh
```

Windows PowerShell:

```powershell
irm https://raw.githubusercontent.com/paveforge/ddb/main/scripts/install.ps1 | iex
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

## AWS connection from a .env file

Use `--env-file` to load AWS credentials and a region for either command. Copy
[.env.example](.env.example) into a private file such as `.env.development`:

```dotenv
AWS_ACCESS_KEY_ID=your-access-key
AWS_SECRET_ACCESS_KEY=your-secret-key
AWS_REGION=us-east-1
# Include this only for temporary credentials:
# AWS_SESSION_TOKEN=your-session-token
```

```sh
ddb export --env-file .env.production --table users --output users.jsonl.gz
```

```sh
ddb import --env-file .env.development --table users-dev \
  --input users.jsonl.gz --overwrite
```

The file is loaded only when explicitly selected; `.env` is not discovered
automatically. `.env` and `.env.*` files are ignored by Git, except `.env.example`.

- Existing process environment values override file values for each variable,
  including explicitly empty values. `AWS_DEFAULT_REGION` and legacy credential
  aliases in the process environment are also respected.
- `--region` overrides the region from either environment source.
- `--env-file` and `--profile` cannot be combined. Standard profiles and IAM roles
  remain available when no effective static credentials are supplied.
- The effective access key and secret key must be supplied together; a session
  token requires both. Remove an inherited `AWS_SESSION_TOKEN` or set it empty
  when switching from temporary credentials to a static key pair.
- Loading a file does not change the process environment. Only
  `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN`, and `AWS_REGION`
  are read from the file; other assignments are ignored.

Files support comments, blank lines, single/double quotes, optional `export`
prefixes, and Windows line endings. Unquoted inline comments start with `#`
after whitespace; quoted values preserve `#`. Double quotes support `\n`, `\r`,
`\t`, `\\`, and `\"` escapes. Values are literal: no variable expansion, shell
execution, or multiline values. Duplicate variables and malformed lines are
errors; diagnostics include line numbers without exposing values. Files are
limited to 1 MiB and lines to 64 KiB.

## Custom endpoints and DynamoDB Local

Both import and export support `--endpoint-url` to override the DynamoDB service
URL. For a DynamoDB Local instance at `http://localhost:8000`:

```sh
ddb export --table users --output users.jsonl \
  --endpoint-url http://localhost:8000 --region us-east-1
```

```sh
ddb import --table users-dev --input users.jsonl --overwrite \
  --endpoint-url http://localhost:8000 --region us-east-1
```

The endpoint must be an absolute HTTP or HTTPS URL. A region and credentials are
still required; DynamoDB Local accepts dummy credentials. For local development,
set these in your shell before running the commands above:

```sh
export AWS_ACCESS_KEY_ID=local
export AWS_SECRET_ACCESS_KEY=local
```

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
