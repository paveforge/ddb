package ddbkit_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/paveforge/ddb/ddbkit"
)

type fakeClient struct {
	name            string
	exists          bool
	writes, creates int
	failWrite       bool
}

func (f *fakeClient) DescribeTable(_ context.Context, in *dynamodb.DescribeTableInput, _ ...func(*dynamodb.Options)) (*dynamodb.DescribeTableOutput, error) {
	f.name = *in.TableName
	if !f.exists {
		return nil, &types.ResourceNotFoundException{}
	}
	return &dynamodb.DescribeTableOutput{Table: &types.TableDescription{TableName: in.TableName, TableStatus: types.TableStatusActive, AttributeDefinitions: []types.AttributeDefinition{{AttributeName: aws.String("id"), AttributeType: types.ScalarAttributeTypeS}}, KeySchema: []types.KeySchemaElement{{AttributeName: aws.String("id"), KeyType: types.KeyTypeHash}}}}, nil
}
func (f *fakeClient) CreateTable(_ context.Context, in *dynamodb.CreateTableInput, _ ...func(*dynamodb.Options)) (*dynamodb.CreateTableOutput, error) {
	f.creates++
	f.exists = true
	f.name = *in.TableName
	return &dynamodb.CreateTableOutput{}, nil
}
func (f *fakeClient) Scan(context.Context, *dynamodb.ScanInput, ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error) {
	return &dynamodb.ScanOutput{}, nil
}
func (f *fakeClient) BatchWriteItem(ctx context.Context, in *dynamodb.BatchWriteItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.BatchWriteItemOutput, error) {
	if f.failWrite {
		return nil, &types.ResourceNotFoundException{}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for _, items := range in.RequestItems {
		f.writes += len(items)
	}
	return &dynamodb.BatchWriteItemOutput{}, nil
}

func TestLoadCreatesOverridesAndWaits(t *testing.T) {
	f := &fakeClient{}
	result, err := ddbkit.Load(context.Background(), f, ddbkit.Options{Table: "suite-users", TableFile: "testdata/table.json", Input: "testdata/users.jsonl", Truncate: true, Recovery: filepath.Join(t.TempDir(), "recovery.jsonl")})
	if err != nil {
		t.Fatal(err)
	}
	if f.creates != 1 || f.writes != 1 || f.name != "suite-users" || result.Confirmed != 1 || result.Phase != "complete" || !result.TruncateComplete {
		t.Fatalf("client=%+v result=%+v", f, result)
	}
}
func TestInvalidFixtureDoesNotMutate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invalid.jsonl")
	if err := os.WriteFile(path, []byte("invalid\n"), 0600); err != nil {
		t.Fatal(err)
	}
	f := &fakeClient{exists: true}
	_, err := ddbkit.Load(context.Background(), f, ddbkit.Options{Table: "users", Input: path, Truncate: true})
	if err == nil || f.writes != 0 || f.creates != 0 {
		t.Fatalf("err=%v client=%+v", err, f)
	}
}
func TestDefaultOverwriteAndCancellation(t *testing.T) {
	f := &fakeClient{exists: true}
	o := ddbkit.Options{Table: "users", Input: "testdata/users.jsonl", Recovery: filepath.Join(t.TempDir(), "recovery.jsonl")}
	for i := 0; i < 2; i++ {
		if _, err := ddbkit.Load(context.Background(), f, o); err != nil {
			t.Fatal(err)
		}
	}
	if f.writes != 2 {
		t.Fatal(f.writes)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ddbkit.Load(ctx, f, o); err == nil {
		t.Fatal("expected cancellation")
	}
}

func TestWriteFailureReturnsRecovery(t *testing.T) {
	f := &fakeClient{exists: true, failWrite: true}
	recovery := filepath.Join(t.TempDir(), "recovery.jsonl")
	result, err := ddbkit.Load(context.Background(), f, ddbkit.Options{Table: "users", Input: "testdata/users.jsonl", Recovery: recovery})
	var missing *types.ResourceNotFoundException
	if !errors.As(err, &missing) || result.Confirmed != 0 || result.Recovery != recovery || !result.RecoveryComplete {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if data, e := os.ReadFile(recovery); e != nil || len(data) == 0 {
		t.Fatalf("recovery: %s %v", data, e)
	}
}
