package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/paveforge/ddb/ddbkit"
	"github.com/paveforge/ddb/internal/report"
	"github.com/paveforge/ddb/internal/transfer"
)

func TestDynamoDBLocal(t *testing.T) {
	endpoint := os.Getenv("DDB_TEST_ENDPOINT")
	if endpoint == "" {
		t.Skip("set DDB_TEST_ENDPOINT to DynamoDB Local")
	}
	ctx := context.Background()
	client := dynamodb.New(dynamodb.Options{Region: "us-east-1", BaseEndpoint: aws.String(endpoint), Credentials: aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
		return aws.Credentials{AccessKeyID: "local", SecretAccessKey: "local"}, nil
	}), RetryMaxAttempts: 1})
	readyCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	for {
		_, e := client.ListTables(readyCtx, &dynamodb.ListTablesInput{})
		if e == nil {
			break
		}
		select {
		case <-readyCtx.Done():
			cancel()
			t.Fatalf("DynamoDB Local not ready: %v", e)
		case <-time.After(100 * time.Millisecond):
		}
	}
	cancel()
	name := fmt.Sprintf("ddb-test-%d", time.Now().UnixNano())
	dir := t.TempDir()
	definition := filepath.Join(dir, "table.json")
	def := map[string]any{"TableName": name, "BillingMode": "PAY_PER_REQUEST", "AttributeDefinitions": []map[string]string{{"AttributeName": "id", "AttributeType": "S"}}, "KeySchema": []map[string]string{{"AttributeName": "id", "KeyType": "HASH"}}}
	b, _ := json.Marshal(def)
	os.WriteFile(definition, b, 0600)
	t.Cleanup(func() { client.DeleteTable(ctx, &dynamodb.DeleteTableInput{TableName: aws.String(name)}) })
	path := filepath.Join(dir, "input.jsonl")
	f, _ := os.Create(path)
	for i := 0; i < 205; i++ {
		fmt.Fprintf(f, "{\"Item\":{\"id\":{\"S\":\"%d\"},\"n\":{\"N\":\"12345678901234567890123456789012345678\"},\"b\":{\"B\":\"AQID\"},\"ss\":{\"SS\":[\"a\",\"b\"]},\"ns\":{\"NS\":[\"1\",\"2\"]},\"bs\":{\"BS\":[\"AQ==\"]},\"m\":{\"M\":{\"l\":{\"L\":[{\"BOOL\":true},{\"NULL\":true},{\"S\":\"\"}]}}}}}\n", i)
	}
	f.Close()
	retry := transfer.Retry{Attempts: 4, Timeout: 10 * time.Second}
	o := transfer.ImportOptions{TableFile: definition, Input: path, Format: "dynamodb-jsonl", Overwrite: true, Concurrency: 3, Retry: retry, WaitTimeout: 30 * time.Second}
	s := &report.Summary{}
	if e := transfer.Import(ctx, client, o, s); e != nil {
		t.Fatal(e)
	}
	if !s.Created || s.Confirmed != 205 {
		t.Fatal(s)
	}
	out := filepath.Join(dir, "out.gz")
	if e := transfer.Export(ctx, client, transfer.ExportOptions{Table: name, Output: out, Format: "dynamodb-jsonl", PageSize: 17, Consistent: true, Retry: retry}, &report.Summary{}); e != nil {
		t.Fatal(e)
	}
	o.TableFile = ""
	o.Table = name
	o.Input = out
	o.Compression = "auto"
	o.Truncate = true
	o.ConfirmTable = name
	s = &report.Summary{}
	if e := transfer.Import(ctx, client, o, s); e != nil {
		t.Fatal(e)
	}
	if s.Deleted != 205 || s.Confirmed != 205 {
		t.Fatal(s)
	}
	scan, e := client.Scan(ctx, &dynamodb.ScanInput{TableName: aws.String(name), Select: types.SelectCount, ConsistentRead: aws.Bool(true)})
	if e != nil || scan.Count != 205 {
		t.Fatalf("count=%v err=%v", scan, e)
	}
	// Exercise the public setup API against an existing populated table.
	fixture := filepath.Join(dir, "single.jsonl")
	if err := os.WriteFile(fixture, []byte("{\"Item\":{\"id\":{\"S\":\"fixture\"}}}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	setupCtx, setupCancel := context.WithTimeout(ctx, 30*time.Second)
	defer setupCancel()
	for attempt := 0; attempt < 2; attempt++ {
		result, err := ddbkit.Load(setupCtx, client, ddbkit.Options{Table: name, Input: fixture, Truncate: true})
		if err != nil {
			t.Fatalf("ddbkit load: %v (%+v)", err, result)
		}
		if result.Confirmed != 1 || !result.TruncateComplete {
			t.Fatal(result)
		}
		if attempt == 0 && result.Deleted != 205 {
			t.Fatal(result)
		}
	}
	scan, e = client.Scan(ctx, &dynamodb.ScanInput{TableName: aws.String(name), Select: types.SelectCount, ConsistentRead: aws.Bool(true)})
	if e != nil || scan.Count != 1 {
		t.Fatalf("ddbkit count=%v err=%v", scan, e)
	}

	isolated := name + "-kit"
	t.Cleanup(func() { client.DeleteTable(ctx, &dynamodb.DeleteTableInput{TableName: aws.String(isolated)}) })
	result, err := ddbkit.Load(setupCtx, client, ddbkit.Options{Table: isolated, TableFile: definition, Input: fixture, Truncate: true})
	if err != nil || !result.Created || result.Table != isolated || result.Confirmed != 1 {
		t.Fatalf("ddbkit creation result=%+v err=%v", result, err)
	}

}
