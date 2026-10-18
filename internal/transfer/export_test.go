package transfer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/paveforge/go-ddb-cli/internal/format"
	"github.com/paveforge/go-ddb-cli/internal/report"
)

type mockAPI struct {
	scan     func(context.Context, *dynamodb.ScanInput) (*dynamodb.ScanOutput, error)
	describe func(context.Context, *dynamodb.DescribeTableInput) (*dynamodb.DescribeTableOutput, error)
	create   func(context.Context, *dynamodb.CreateTableInput) (*dynamodb.CreateTableOutput, error)
	batch    func(context.Context, *dynamodb.BatchWriteItemInput) (*dynamodb.BatchWriteItemOutput, error)
}

func (m *mockAPI) Scan(c context.Context, i *dynamodb.ScanInput, _ ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error) {
	return m.scan(c, i)
}
func (m *mockAPI) DescribeTable(c context.Context, i *dynamodb.DescribeTableInput, _ ...func(*dynamodb.Options)) (*dynamodb.DescribeTableOutput, error) {
	return m.describe(c, i)
}
func (m *mockAPI) CreateTable(c context.Context, i *dynamodb.CreateTableInput, _ ...func(*dynamodb.Options)) (*dynamodb.CreateTableOutput, error) {
	return m.create(c, i)
}
func (m *mockAPI) BatchWriteItem(c context.Context, i *dynamodb.BatchWriteItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.BatchWriteItemOutput, error) {
	return m.batch(c, i)
}
func testRetry() Retry { return Retry{Attempts: 3, Timeout: time.Second} }
func TestExportPaginationGzip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.gz")
	calls := 0
	m := &mockAPI{scan: func(_ context.Context, i *dynamodb.ScanInput) (*dynamodb.ScanOutput, error) {
		calls++
		r := &dynamodb.ScanOutput{Items: []map[string]types.AttributeValue{{"id": &types.AttributeValueMemberS{Value: "x"}}}, ScannedCount: 1}
		if calls == 1 {
			r.LastEvaluatedKey = r.Items[0]
		} else if len(i.ExclusiveStartKey) == 0 {
			t.Fatal("cursor not forwarded")
		}
		return r, nil
	}}
	s := &report.Summary{}
	if e := Export(context.Background(), m, ExportOptions{Table: "test", Output: path, Format: "dynamodb-jsonl", PageSize: 10, Retry: testRetry()}, s); e != nil {
		t.Fatal(e)
	}
	r, e := Input(path, "auto")
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	n := 0
	if e = format.Decode(r, "dynamodb-jsonl", func(format.Item) error { n++; return nil }); e != nil || n != 2 {
		t.Fatalf("%d %v", n, e)
	}
	if s.Partial != "" || s.Confirmed != 2 {
		t.Fatal(s)
	}
}
func TestExportFailureAndNoClobber(t *testing.T) {
	p := filepath.Join(t.TempDir(), "out")
	m := &mockAPI{scan: func(context.Context, *dynamodb.ScanInput) (*dynamodb.ScanOutput, error) {
		return nil, errors.New("denied")
	}}
	o := ExportOptions{Table: "test", Output: p, Format: "json", PageSize: 10, Retry: testRetry()}
	if e := Export(context.Background(), m, o, &report.Summary{}); e == nil {
		t.Fatal("expected error")
	}
	if _, e := os.Stat(p); !os.IsNotExist(e) {
		t.Fatal("published incomplete file")
	}
	if _, e := os.Stat(p + ".partial"); e != nil {
		t.Fatal(e)
	}
	os.WriteFile(p, []byte("keep"), 0600)
	if e := Export(context.Background(), m, o, &report.Summary{}); e == nil {
		t.Fatal("clobbered")
	}
	b, _ := os.ReadFile(p)
	if strings.TrimSpace(string(b)) != "keep" {
		t.Fatal(string(b))
	}
	_ = aws.String("test")
}
