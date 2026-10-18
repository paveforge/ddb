# Contributing

Use Go 1.24 or newer. Keep changes focused, run `gofmt`, and add tests for changed
transfer behavior, especially partial failures and recovery.

```sh
go test -race ./...
go vet ./...
go build ./cmd/ddb
```

Run integration tests against an isolated DynamoDB Local instance:

```sh
docker run --rm -p 127.0.0.1:8000:8000 amazon/dynamodb-local \
  -jar DynamoDBLocal.jar -inMemory -sharedDb
# In another terminal:
DDB_TEST_ENDPOINT=http://127.0.0.1:8000 go test -race ./integration -v
```

Only point DDB_TEST_ENDPOINT at a disposable local instance: tests create and
delete tables. Ordinary tests use mocks and installer fixtures without AWS access.
On Windows run `./scripts/test-install.ps1` in PowerShell as well.

Release maintainers: run `goreleaser check` and
`goreleaser release --snapshot --clean` to verify all six binaries. Publishing is
triggered by a `v*` tag; publish only after tests and review. Do not put AWS keys,
production datasets, or private configuration in tests or issues.
