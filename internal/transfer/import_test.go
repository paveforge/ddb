package transfer

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/paveforge/go-ddb-cli/internal/format"
	"github.com/paveforge/go-ddb-cli/internal/report"
)

func description() *types.TableDescription {
	return &types.TableDescription{TableName: aws.String("users"), TableStatus: types.TableStatusActive, AttributeDefinitions: []types.AttributeDefinition{{AttributeName: aws.String("id"), AttributeType: types.ScalarAttributeTypeS}}, KeySchema: []types.KeySchemaElement{{AttributeName: aws.String("id"), KeyType: types.KeyTypeHash}}}
}
func input(t *testing.T, n int) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "in.jsonl")
	f, e := os.Create(p)
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < n; i++ {
		fmt.Fprintf(f, "{\"Item\":{\"id\":{\"S\":\"%d\"}}}\n", i)
	}
	f.Close()
	return p
}
func options(path string) ImportOptions {
	return ImportOptions{Table: "users", Input: path, Format: "dynamodb-jsonl", Overwrite: true, Concurrency: 2, Retry: testRetry(), WaitTimeout: time.Second}
}
func mockImport() *mockAPI {
	return &mockAPI{describe: func(context.Context, *dynamodb.DescribeTableInput) (*dynamodb.DescribeTableOutput, error) {
		return &dynamodb.DescribeTableOutput{Table: description()}, nil
	}, batch: func(context.Context, *dynamodb.BatchWriteItemInput) (*dynamodb.BatchWriteItemOutput, error) {
		return &dynamodb.BatchWriteItemOutput{}, nil
	}}
}
func TestImportPreflightBeforeTruncate(t *testing.T) {
	p := input(t, 1)
	f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0600)
	f.WriteString("broken\n")
	f.Close()
	m := mockImport()
	m.scan = func(context.Context, *dynamodb.ScanInput) (*dynamodb.ScanOutput, error) {
		t.Fatal("deleted before validation")
		return nil, nil
	}
	o := options(p)
	o.Truncate = true
	o.ConfirmTable = "users"
	s := &report.Summary{}
	if e := Import(context.Background(), m, o, s); e == nil {
		t.Fatal("accepted malformed input")
	}
	if s.TruncateStarted {
		t.Fatal(s)
	}
}
func TestImportRecoveryPartialResponse(t *testing.T) {
	p := input(t, 30)
	m := mockImport()
	calls := 0
	m.batch = func(_ context.Context, i *dynamodb.BatchWriteItemInput) (*dynamodb.BatchWriteItemOutput, error) {
		calls++
		r := i.RequestItems["users"]
		if calls == 1 {
			return &dynamodb.BatchWriteItemOutput{UnprocessedItems: map[string][]types.WriteRequest{"users": r[:1]}}, nil
		}
		if len(r) != 1 {
			t.Fatalf("retried successful records: %d", len(r))
		}
		return nil, errors.New("permission denied")
	}
	o := options(p)
	o.Concurrency = 1
	s := &report.Summary{}
	if e := Import(context.Background(), m, o, s); e == nil {
		t.Fatal("expected failure")
	}
	if s.Confirmed != 24 || s.Unresolved != 1 || s.NotAttempted != 5 || !s.RecoveryComplete {
		t.Fatalf("%+v", s)
	}
	f, e := os.Open(s.Recovery)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	n := 0
	if e = format.Decode(f, "dynamodb-jsonl", func(format.Item) error { n++; return nil }); e != nil || n != 6 {
		t.Fatalf("recovery records=%d err=%v", n, e)
	}
}
func TestImportDuplicateOrdering(t *testing.T) {
	p := input(t, 0)
	os.WriteFile(p, []byte("{\"Item\":{\"id\":{\"S\":\"same\"},\"v\":{\"N\":\"1\"}}}\n{\"Item\":{\"id\":{\"S\":\"same\"},\"v\":{\"N\":\"2\"}}}\n"), 0600)
	m := mockImport()
	last := ""
	m.batch = func(_ context.Context, i *dynamodb.BatchWriteItemInput) (*dynamodb.BatchWriteItemOutput, error) {
		for _, r := range i.RequestItems["users"] {
			v := r.PutRequest.Item["v"].(*types.AttributeValueMemberN).Value
			if last == "2" {
				t.Fatal("out of order")
			}
			last = v
		}
		return &dynamodb.BatchWriteItemOutput{}, nil
	}
	s := &report.Summary{}
	if e := Import(context.Background(), m, options(p), s); e != nil {
		t.Fatal(e)
	}
	if last != "2" || s.Confirmed != 2 {
		t.Fatal(s)
	}
	if _, e := os.Stat(p + ".recovery.jsonl"); !os.IsNotExist(e) {
		t.Fatal("left recovery on success")
	}
}
func TestImportCreateAndDryRun(t *testing.T) {
	for _, dry := range []bool{true, false} {
		t.Run(fmt.Sprint(dry), func(t *testing.T) {
			p := input(t, 3)
			def := filepath.Join(t.TempDir(), "table.json")
			os.WriteFile(def, []byte(`{"TableName":"users","BillingMode":"PAY_PER_REQUEST","AttributeDefinitions":[{"AttributeName":"id","AttributeType":"S"}],"KeySchema":[{"AttributeName":"id","KeyType":"HASH"}]}`), 0600)
			m := mockImport()
			created := false
			m.describe = func(context.Context, *dynamodb.DescribeTableInput) (*dynamodb.DescribeTableOutput, error) {
				if !created {
					return nil, &types.ResourceNotFoundException{}
				}
				return &dynamodb.DescribeTableOutput{Table: description()}, nil
			}
			m.create = func(context.Context, *dynamodb.CreateTableInput) (*dynamodb.CreateTableOutput, error) {
				if dry {
					t.Fatal("dry run created table")
				}
				created = true
				return &dynamodb.CreateTableOutput{}, nil
			}
			o := options(p)
			o.Table = ""
			o.TableFile = def
			o.DryRun = dry
			s := &report.Summary{}
			if e := Import(context.Background(), m, o, s); e != nil {
				t.Fatal(e)
			}
			if s.Created == dry || s.Validated != 3 {
				t.Fatal(s)
			}
		})
	}
}
func TestTruncationFailureStopsImport(t *testing.T) {
	p := input(t, 2)
	m := mockImport()
	m.scan = func(_ context.Context, i *dynamodb.ScanInput) (*dynamodb.ScanOutput, error) {
		if i.ProjectionExpression == nil || len(i.ExpressionAttributeNames) != 1 {
			t.Fatal("not a key-only scan")
		}
		return &dynamodb.ScanOutput{Items: []map[string]types.AttributeValue{{"id": &types.AttributeValueMemberS{Value: "old"}}}}, nil
	}
	m.batch = func(_ context.Context, i *dynamodb.BatchWriteItemInput) (*dynamodb.BatchWriteItemOutput, error) {
		if i.RequestItems["users"][0].PutRequest != nil {
			t.Fatal("import started after truncation failed")
		}
		return nil, errors.New("denied")
	}
	o := options(p)
	o.Truncate = true
	o.ConfirmTable = "users"
	s := &report.Summary{}
	if e := Import(context.Background(), m, o, s); e == nil {
		t.Fatal("expected failure")
	}
	if s.Recovery != "" || s.TruncateComplete {
		t.Fatal(s)
	}
}
func TestBatchRetryAndCancellation(t *testing.T) {
	m := mockImport()
	calls := 0
	m.batch = func(context.Context, *dynamodb.BatchWriteItemInput) (*dynamodb.BatchWriteItemOutput, error) {
		calls++
		if calls < 2 {
			return nil, &types.ProvisionedThroughputExceededException{}
		}
		return &dynamodb.BatchWriteItemOutput{}, nil
	}
	req := []types.WriteRequest{{PutRequest: &types.PutRequest{Item: format.Item{"id": &types.AttributeValueMemberS{Value: "x"}}}}}
	r := batchWrite(context.Background(), m, "users", req, testRetry(), NewPacer(0))
	if r.err != nil || !r.confirmed[0] || r.retries != 1 {
		t.Fatal(r)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r = batchWrite(ctx, m, "users", req, testRetry(), NewPacer(0))
	if r.err == nil {
		t.Fatal("ignored cancellation")
	}
}
func TestLargeStreamingImport(t *testing.T) {
	if testing.Short() {
		t.Skip("large generated input")
	}
	p := input(t, 20000)
	m := mockImport()
	var mu sync.Mutex
	count := 0
	m.batch = func(_ context.Context, i *dynamodb.BatchWriteItemInput) (*dynamodb.BatchWriteItemOutput, error) {
		mu.Lock()
		defer mu.Unlock()
		n := len(i.RequestItems["users"])
		if n > 25 {
			t.Fatal("oversized batch")
		}
		count += n
		return &dynamodb.BatchWriteItemOutput{}, nil
	}
	s := &report.Summary{}
	if e := Import(context.Background(), m, options(p), s); e != nil {
		t.Fatal(e)
	}
	if count != 20000 || s.NotAttempted != 0 {
		t.Fatal(s)
	}
}
func TestSchemaChangeAfterPreflight(t *testing.T) {
	p := input(t, 2)
	m := mockImport()
	calls := 0
	m.describe = func(context.Context, *dynamodb.DescribeTableInput) (*dynamodb.DescribeTableOutput, error) {
		calls++
		d := description()
		if calls == 2 {
			d.KeySchema[0].AttributeName = aws.String("other")
		}
		return &dynamodb.DescribeTableOutput{Table: d}, nil
	}
	m.batch = func(context.Context, *dynamodb.BatchWriteItemInput) (*dynamodb.BatchWriteItemOutput, error) {
		t.Fatal("wrote changed schema")
		return nil, nil
	}
	e := Import(context.Background(), m, options(p), &report.Summary{})
	if e == nil || !strings.Contains(e.Error(), "schema changed") {
		t.Fatal(e)
	}
}

func TestRecoveryPreservesUnattemptedOnCancel(t *testing.T) {
	p := input(t, 30)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := mockImport()
	m.batch = func(context.Context, *dynamodb.BatchWriteItemInput) (*dynamodb.BatchWriteItemOutput, error) {
		cancel()
		return nil, context.Canceled
	}
	o := options(p)
	o.Concurrency = 1
	s := &report.Summary{}
	e := Import(ctx, m, o, s)
	if !errors.Is(e, context.Canceled) || !s.RecoveryComplete || s.Unresolved != 25 || s.NotAttempted != 5 {
		t.Fatalf("err=%v summary=%+v", e, s)
	}
}
func TestInputSnapshotSurvivesSourceChange(t *testing.T) {
	p := input(t, 3)
	m := mockImport()
	calls := 0
	m.describe = func(context.Context, *dynamodb.DescribeTableInput) (*dynamodb.DescribeTableOutput, error) {
		calls++
		if calls == 2 {
			os.WriteFile(p, []byte("corrupted after preflight"), 0600)
		}
		return &dynamodb.DescribeTableOutput{Table: description()}, nil
	}
	s := &report.Summary{}
	if e := Import(context.Background(), m, options(p), s); e != nil || s.Confirmed != 3 {
		t.Fatalf("%v %+v", e, s)
	}
}

func TestSiblingCancellationRetainsFailure(t *testing.T) {
	m := mockImport()
	started := make(chan struct{})
	var once sync.Once
	m.batch = func(c context.Context, i *dynamodb.BatchWriteItemInput) (*dynamodb.BatchWriteItemOutput, error) {
		if i.RequestItems["users"][0].PutRequest.Item["id"].(*types.AttributeValueMemberS).Value == "0" {
			<-started
			return nil, errors.New("denied")
		}
		once.Do(func() { close(started) })
		<-c.Done()
		return nil, c.Err()
	}
	p := input(t, 50)
	s := &report.Summary{}
	e := Import(context.Background(), m, options(p), s)
	if report.ExitCode(e) != 3 || !strings.Contains(e.Error(), "denied") {
		t.Fatalf("exit=%d err=%v", report.ExitCode(e), e)
	}
}
func TestWireSizeSplitsBatches(t *testing.T) {
	m := mockImport()
	var mu sync.Mutex
	calls := 0
	m.batch = func(_ context.Context, i *dynamodb.BatchWriteItemInput) (*dynamodb.BatchWriteItemOutput, error) {
		size := 0
		for _, r := range i.RequestItems["users"] {
			b, _ := format.Marshal(r.PutRequest.Item, true)
			size += len(b) + 32
		}
		if size > 16*1024*1024-4096 {
			t.Error("oversized wire batch")
		}
		mu.Lock()
		calls++
		mu.Unlock()
		return &dynamodb.BatchWriteItemOutput{}, nil
	}
	wave := []record{}
	payload := strings.Repeat("\x00", 350*1024)
	for i := 0; i < 10; i++ {
		wave = append(wave, record{index: int64(i), item: format.Item{"id": &types.AttributeValueMemberS{Value: fmt.Sprint(i)}, "payload": &types.AttributeValueMemberS{Value: payload}}})
	}
	j, e := os.CreateTemp(t.TempDir(), "journal")
	if e != nil {
		t.Fatal(e)
	}
	defer j.Close()
	j.Truncate(10)
	s := &report.Summary{NotAttempted: 10}
	if e = writeWave(context.Background(), m, options("unused"), wave, NewPacer(0), j, s); e != nil {
		t.Fatal(e)
	}
	if calls < 2 || s.Confirmed != 10 {
		t.Fatalf("calls=%d summary=%+v", calls, s)
	}
}

// The source exceeds the heap allowance, catching accidental whole-file loading.
func TestImportBoundedHeap(t *testing.T) {
	if testing.Short() {
		t.Skip("large resource test")
	}
	p := input(t, 0)
	f, e := os.OpenFile(p, os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	w := bufio.NewWriter(f)
	payload := strings.Repeat("x", 8192)
	for i := 0; i < 15000; i++ {
		fmt.Fprintf(w, "{\"Item\":{\"id\":{\"S\":\"%d\"},\"payload\":{\"S\":\"%s\"}}}\n", i, payload)
	}
	w.Flush()
	f.Close()
	runtime.GC()
	var baseline runtime.MemStats
	runtime.ReadMemStats(&baseline)
	var mu sync.Mutex
	peak := baseline.HeapAlloc
	m := mockImport()
	m.batch = func(context.Context, *dynamodb.BatchWriteItemInput) (*dynamodb.BatchWriteItemOutput, error) {
		var stats runtime.MemStats
		runtime.ReadMemStats(&stats)
		mu.Lock()
		if stats.HeapAlloc > peak {
			peak = stats.HeapAlloc
		}
		mu.Unlock()
		return &dynamodb.BatchWriteItemOutput{}, nil
	}
	s := &report.Summary{}
	if e := Import(context.Background(), m, options(p), s); e != nil {
		t.Fatal(e)
	}
	if peak > baseline.HeapAlloc+64*1024*1024 {
		t.Fatalf("heap grew by %d bytes for a streaming import", peak-baseline.HeapAlloc)
	}
	t.Logf("source >117 MiB; sampled heap growth %.1f MiB", float64(peak-baseline.HeapAlloc)/(1024*1024))
}
