package transfer

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/paveforge/go-ddb-cli/internal/format"
	"github.com/paveforge/go-ddb-cli/internal/report"
	"github.com/paveforge/go-ddb-cli/internal/table"
)

type ImportOptions struct {
	Table, TableFile, Input, Format, Compression, Recovery string
	Overwrite, Truncate, DryRun                            bool
	ConfirmTable                                           string
	Concurrency                                            int
	WriteRate                                              float64
	Retry                                                  Retry
	WaitTimeout                                            time.Duration
	Progress                                               func(report.Summary)
}
type record struct {
	index int64
	item  format.Item
	key   string
}

func identity(item format.Item, d *types.TableDescription) string {
	k := table.Key(item, d)
	for n, a := range k {
		if v, ok := a.(*types.AttributeValueMemberN); ok {
			f, _ := new(big.Rat).SetString(v.Value)
			k[n] = &types.AttributeValueMemberN{Value: f.RatString()}
		}
	}
	b, _ := format.Marshal(k, true)
	return string(b)
}
func samePath(a, b string) bool {
	x, e := filepath.Abs(a)
	if e != nil {
		return false
	}
	y, e := filepath.Abs(b)
	return e == nil && x == y
}

func Import(ctx context.Context, api API, o ImportOptions, s *report.Summary) (err error) {
	s.Phase = "preflight"
	s.DryRun = o.DryRun
	bad := func(e error) error { return report.Fail(2, s.Phase, e) }
	if (o.Table == "") == (o.TableFile == "") {
		return bad(fmt.Errorf("exactly one of --table or --table-file is required"))
	}
	if o.Concurrency < 1 || o.Concurrency > 64 || o.WriteRate < 0 || o.WaitTimeout <= 0 {
		return bad(fmt.Errorf("concurrency must be 1..64, write rate nonnegative, and wait timeout positive"))
	}
	if e := o.Retry.Validate(); e != nil {
		return bad(e)
	}
	var def *dynamodb.CreateTableInput
	if o.TableFile != "" {
		def, err = table.Load(o.TableFile)
		if err != nil {
			return bad(err)
		}
		o.Table = aws.ToString(def.TableName)
	}
	s.Table = o.Table
	if o.Truncate {
		if o.ConfirmTable != o.Table {
			return bad(fmt.Errorf("--truncate requires --confirm-table %s", o.Table))
		}
	} else if o.ConfirmTable != "" {
		return bad(fmt.Errorf("--confirm-table requires --truncate"))
	}
	if !o.DryRun && !o.Truncate && !o.Overwrite {
		return bad(fmt.Errorf("import replaces matching items; acknowledge with --overwrite"))
	}
	d, err := retryCall(ctx, o.Retry, &s.Retries, func(c context.Context) (*types.TableDescription, error) { return table.Describe(c, api, o.Table) })
	if err != nil {
		return report.Fail(3, s.Phase, err)
	}
	original := d
	missing := d == nil
	if missing {
		if def == nil {
			return bad(fmt.Errorf("destination table is missing; use --table-file to create it"))
		}
		d = table.FromDefinition(def)
	} else {
		if !table.Ready(d) {
			return bad(fmt.Errorf("destination table/indexes are not ACTIVE"))
		}
		if def != nil {
			if err = table.Compatible(def, d); err != nil {
				return bad(err)
			}
		}
	}
	in, err := Input(o.Input, o.Compression)
	if err != nil {
		return report.Fail(4, s.Phase, err)
	}
	defer in.Close()
	stage, err := os.CreateTemp("", "ddb-validated-*.jsonl")
	if err != nil {
		return report.Fail(4, s.Phase, err)
	}
	defer func() { stage.Close(); os.Remove(stage.Name()) }()
	hash := sha256.New()
	bw := bufio.NewWriterSize(stage, 64*1024)
	enc, _ := format.NewEncoder(bw, "dynamodb-jsonl")
	err = format.Decode(io.TeeReader(in, hash), o.Format, func(item format.Item) error {
		if e := ctx.Err(); e != nil {
			return e
		}
		if e := table.ValidateItem(item, d); e != nil {
			return fmt.Errorf("record %d: %w", s.Validated+1, e)
		}
		if e := enc.Write(item); e != nil {
			return report.Fail(4, "preflight", e)
		}
		s.Validated++
		return nil
	})
	if err != nil {
		return bad(err)
	}
	if err = bw.Flush(); err != nil {
		return report.Fail(4, s.Phase, err)
	}
	if err = stage.Sync(); err != nil {
		return report.Fail(4, s.Phase, err)
	}
	if _, err = stage.Seek(0, 0); err != nil {
		return report.Fail(4, s.Phase, err)
	}
	s.InputSHA256 = hex.EncodeToString(hash.Sum(nil))
	if st, e := stage.Stat(); e == nil {
		s.Bytes = st.Size()
	}
	s.NotAttempted = s.Validated
	if o.DryRun {
		s.Phase = "complete"
		return nil
	}
	if o.Recovery == "" {
		o.Recovery = o.Input + ".recovery.jsonl"
	}
	if samePath(o.Input, o.Recovery) || samePath(o.TableFile, o.Recovery) {
		return bad(fmt.Errorf("recovery path must differ from input and definition"))
	}
	recovery, err := report.NewFile(o.Recovery)
	if err != nil {
		return report.Fail(4, s.Phase, err)
	}
	imported := false
	keepRecovery := false
	defer func() {
		if recovery != nil {
			recovery.Close()
		}
		if !keepRecovery {
			os.Remove(o.Recovery)
		}
	}()
	journal, err := os.CreateTemp("", "ddb-confirmed-*")
	if err != nil {
		return report.Fail(4, s.Phase, err)
	}
	defer func() { journal.Close(); os.Remove(journal.Name()) }()
	if err = journal.Truncate(s.Validated); err != nil {
		return report.Fail(4, s.Phase, err)
	}
	defer func() {
		if err != nil && imported {
			keepRecovery = true
			s.Recovery = o.Recovery
			s.RecoveryComplete = false
			if _, e := stage.Seek(0, 0); e != nil {
				err = errors.Join(err, fmt.Errorf("recovery: %w", e))
				return
			}
			b := bufio.NewWriter(recovery)
			eout, _ := format.NewEncoder(b, "dynamodb-jsonl")
			var index int64
			e := format.Decode(stage, "dynamodb-jsonl", func(item format.Item) error {
				var flag [1]byte
				if _, e := journal.ReadAt(flag[:], index); e != nil {
					return e
				}
				index++
				if flag[0] == 0 {
					return eout.Write(item)
				}
				return nil
			})
			e = errors.Join(e, eout.Close(), b.Flush(), recovery.Sync())
			if ce := recovery.Close(); ce != nil {
				e = errors.Join(e, ce)
			}
			recovery = nil
			if e != nil {
				err = errors.Join(err, fmt.Errorf("recovery file incomplete: %w", e))
			} else {
				s.RecoveryComplete = true
			}
		}
	}()
	if missing {
		s.Phase = "creation"
		_, e := retryCall(ctx, o.Retry, &s.Retries, func(c context.Context) (*dynamodb.CreateTableOutput, error) { return api.CreateTable(c, def) })
		var exists *types.ResourceInUseException
		if e != nil && !errors.As(e, &exists) {
			return report.Fail(3, s.Phase, e)
		}
		s.Created = e == nil
		d, err = table.Wait(ctx, api, o.Table, o.WaitTimeout)
		if err != nil {
			return report.Fail(3, s.Phase, err)
		}
		if err = table.Compatible(def, d); err != nil {
			return bad(err)
		}
	} else {
		// Recheck immediately before mutation; input validation may have taken hours.
		d, err = table.Describe(ctx, api, o.Table)
		if err != nil {
			return report.Fail(3, s.Phase, err)
		}
		if d == nil || !table.Ready(d) {
			return bad(fmt.Errorf("destination changed or is no longer ACTIVE"))
		}
		originalDef := &dynamodb.CreateTableInput{TableName: original.TableName, AttributeDefinitions: original.AttributeDefinitions, KeySchema: original.KeySchema}
		for _, g := range original.GlobalSecondaryIndexes {
			originalDef.GlobalSecondaryIndexes = append(originalDef.GlobalSecondaryIndexes, types.GlobalSecondaryIndex{IndexName: g.IndexName, KeySchema: g.KeySchema, Projection: g.Projection})
		}
		for _, l := range original.LocalSecondaryIndexes {
			originalDef.LocalSecondaryIndexes = append(originalDef.LocalSecondaryIndexes, types.LocalSecondaryIndex{IndexName: l.IndexName, KeySchema: l.KeySchema, Projection: l.Projection})
		}
		if e := table.Compatible(originalDef, d); e != nil {
			return bad(fmt.Errorf("destination schema changed during preflight: %w", e))
		}

		if def != nil {
			if err = table.Compatible(def, d); err != nil {
				return bad(err)
			}
		}
	}
	pacer := NewPacer(o.WriteRate)
	if o.Truncate {
		s.Phase = "truncation"
		s.TruncateStarted = true
		if err = truncate(ctx, api, o, d, pacer, s); err != nil {
			return report.Fail(3, s.Phase, err)
		}
		s.TruncateComplete = true
	}
	s.Phase = "import"
	imported = true
	wave := make([]record, 0, o.Concurrency*25)
	seen := map[string]bool{}
	var index int64
	flush := func() error {
		if len(wave) == 0 {
			return nil
		}
		e := writeWave(ctx, api, o, wave, pacer, journal, s)
		wave = wave[:0]
		clear(seen)
		if o.Progress != nil {
			o.Progress(*s)
		}
		return e
	}
	err = format.Decode(stage, "dynamodb-jsonl", func(item format.Item) error {
		if e := ctx.Err(); e != nil {
			return e
		}
		key := identity(item, d)
		if seen[key] || len(wave) == cap(wave) {
			if e := flush(); e != nil {
				return e
			}
		}
		wave = append(wave, record{index: index, item: item, key: key})
		seen[key] = true
		index++
		return nil
	})
	if err == nil {
		err = flush()
	}
	if err != nil {
		return report.Fail(3, s.Phase, err)
	}
	s.Phase = "complete"
	return nil
}

type batchResult struct {
	confirmed []bool
	attempted bool
	retries   int64
	capacity  float64
	err       error
}

func batchWrite(ctx context.Context, api API, name string, requests []types.WriteRequest, r Retry, p *Pacer) batchResult {
	result := batchResult{confirmed: make([]bool, len(requests))}
	pending := append([]types.WriteRequest{}, requests...)
	positions := make([]int, len(requests))
	for i := range positions {
		positions[i] = i
	}
	ctx, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel()
	for attempt := 0; attempt < r.Attempts; attempt++ {
		if result.err = p.Wait(ctx); result.err != nil {
			return result
		}
		result.attempted = true
		out, e := api.BatchWriteItem(ctx, &dynamodb.BatchWriteItemInput{RequestItems: map[string][]types.WriteRequest{name: pending}, ReturnConsumedCapacity: types.ReturnConsumedCapacityTotal})
		if e != nil {
			result.err = e
			if !transient(e) {
				return result
			}
		} else {
			for _, c := range out.ConsumedCapacity {
				result.capacity += aws.ToFloat64(c.CapacityUnits)
			}
			unprocessed := out.UnprocessedItems[name]
			next := []types.WriteRequest{}
			nextPos := []int{}
			for i, req := range pending {
				found := false
				for _, u := range unprocessed {
					if equalRequest(req, u) {
						found = true
						break
					}
				}
				if found {
					next = append(next, req)
					nextPos = append(nextPos, positions[i])
				} else {
					result.confirmed[positions[i]] = true
				}
			}
			pending = next
			positions = nextPos
			if len(pending) == 0 {
				result.err = nil
				return result
			}
			result.err = fmt.Errorf("unprocessed items remain")
		}
		if attempt+1 < r.Attempts {
			result.retries++
			if e = backoff(ctx, attempt); e != nil {
				result.err = e
				return result
			}
		}
	}
	return result
}
func equalRequest(a, b types.WriteRequest) bool {
	var x, y []byte
	if a.PutRequest != nil && b.PutRequest != nil {
		x, _ = format.Marshal(a.PutRequest.Item, true)
		y, _ = format.Marshal(b.PutRequest.Item, true)
	} else if a.DeleteRequest != nil && b.DeleteRequest != nil {
		x, _ = format.Marshal(a.DeleteRequest.Key, true)
		y, _ = format.Marshal(b.DeleteRequest.Key, true)
	} else {
		return false
	}
	return string(x) == string(y)
}
func writeWave(ctx context.Context, api API, o ImportOptions, wave []record, p *Pacer, journal *os.File, s *report.Summary) error {
	count := (len(wave) + 24) / 25
	results := make([]batchResult, count)
	var wg sync.WaitGroup
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	for batch := 0; batch < count; batch++ {
		wg.Add(1)
		go func(b int) {
			defer wg.Done()
			start := b * 25
			end := min(start+25, len(wave))
			req := make([]types.WriteRequest, end-start)
			for i, v := range wave[start:end] {
				req[i] = types.WriteRequest{PutRequest: &types.PutRequest{Item: v.item}}
			}
			results[b] = batchWrite(workCtx, api, o.Table, req, o.Retry, p)
			if results[b].err != nil {
				cancel()
			}
		}(batch)
	}
	wg.Wait()
	var err error
	for b, res := range results {
		s.Retries += res.retries
		s.Capacity += res.capacity
		for i, ok := range res.confirmed {
			rec := wave[b*25+i]
			if ok {
				s.Confirmed++
				s.NotAttempted--
				if _, e := journal.WriteAt([]byte{1}, rec.index); e != nil {
					err = errors.Join(err, report.Fail(4, "journal", e))
				}
			} else if res.attempted {
				s.Unresolved++
				s.NotAttempted--
			}
		}
		err = errors.Join(err, res.err)
	}
	return err
}
func truncate(ctx context.Context, api API, o ImportOptions, d *types.TableDescription, p *Pacer, s *report.Summary) error {
	var cursor format.Item
	names := map[string]string{}
	projection := ""
	for i, k := range d.KeySchema {
		alias := fmt.Sprintf("#k%d", i)
		names[alias] = aws.ToString(k.AttributeName)
		if projection != "" {
			projection += ","
		}
		projection += alias
	}
	for {
		out, e := retryCall(ctx, o.Retry, &s.Retries, func(c context.Context) (*dynamodb.ScanOutput, error) {
			return api.Scan(c, &dynamodb.ScanInput{TableName: aws.String(o.Table), ExclusiveStartKey: cursor, ProjectionExpression: aws.String(projection), ExpressionAttributeNames: names, ConsistentRead: aws.Bool(true), Limit: aws.Int32(100), ReturnConsumedCapacity: types.ReturnConsumedCapacityTotal})
		})
		if e != nil {
			return e
		}
		s.Scanned += int64(out.ScannedCount)
		if out.ConsumedCapacity != nil {
			s.Capacity += aws.ToFloat64(out.ConsumedCapacity.CapacityUnits)
		}
		for start := 0; start < len(out.Items); start += 25 {
			end := min(start+25, len(out.Items))
			req := make([]types.WriteRequest, end-start)
			for i, item := range out.Items[start:end] {
				req[i] = types.WriteRequest{DeleteRequest: &types.DeleteRequest{Key: table.Key(item, d)}}
			}
			r := batchWrite(ctx, api, o.Table, req, o.Retry, p)
			s.Retries += r.retries
			s.Capacity += r.capacity
			for _, ok := range r.confirmed {
				if ok {
					s.Deleted++
				}
			}
			if r.err != nil {
				return r.err
			}
		}
		if o.Progress != nil {
			o.Progress(*s)
		}
		cursor = out.LastEvaluatedKey
		if len(cursor) == 0 {
			return nil
		}
	}
}
