# Transfer formats

`dynamodb-jsonl` (default) is UTF-8 JSON Lines. Each nonempty line is exactly
`{"Item":{...}}`, using DynamoDB AttributeValue tags: S, N, B, BOOL, NULL, SS,
NS, BS, L, M. Binary values use base64; numbers remain decimal strings. Blank
lines are ignored. Empty input represents zero items. S3 manifests and
incremental export envelopes are not supported.

`json` is a JSON array of objects. Numbers are parsed without float conversion;
arrays become lists, objects become maps, null becomes NULL. Binary and sets
cannot be exported in this format without losing their types, so export rejects
them. Use DynamoDB JSON Lines for lossless round trips.

Compression is `auto` (infer `.gz`), `none`, or `gzip`; explicit choices override
the suffix. Input is a local regular file and is fully validated before mutation.
