# Table definitions

Import requires exactly one of `--table` or `--table-file`.

- `--table users-dev`: use an existing table; missing tables are errors.
- `--table-file table.json`: require `TableName` in a DynamoDB `CreateTable`
  request JSON file. Create a missing table and wait for the table/indexes to
  become ACTIVE before importing. Default readiness timeout: 10 minutes.

See [table.json](examples/table.json) for an on-demand table with a simple key.
For provisioned billing, supply positive read/write throughput for the table and
all global secondary indexes. AttributeDefinitions describe key attributes only,
not every attribute in your data.

The parser rejects unknown properties and locally detectable invalid definitions.
AWS is authoritative for remaining CreateTable restrictions. The definition is
limited to 1 MiB. Supported creation settings follow the pinned AWS Go SDK's
CreateTable input, including indexes, billing, throughput, encryption, and tags.
TTL and autoscaling require separate APIs and are not configured.

Existing tables must have compatible primary keys, key attribute types, and local
and global index keys/projections. Index array order and INCLUDE attribute order
are ignored. Extra or missing indexes are incompatible. Billing, throughput,
tags, and encryption are not reconciled. The tool never updates an existing
schema. Input key values are checked against the resolved table/index key types.

If another caller creates the destination during the creation race, `ddb`
re-describes it, waits, and checks compatibility. A newly created table is never
deleted automatically when a later step fails. A network error during creation
can leave the outcome uncertain; inspect the destination before retrying.

`--dry-run` validates input and reads table state without creation, deletion, or
item writes. Success cannot guarantee future permissions, capacity, or service
availability. Write permissions cannot be proved using read-only calls.
