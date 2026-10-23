package transfer

import (
	"bufio"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/paveforge/ddb/internal/format"
	"github.com/paveforge/ddb/internal/report"
)

type ExportOptions struct {
	Table, Output, Format, Compression string
	Force, Consistent                  bool
	PageSize                           int32
	ReadRate                           float64
	Retry                              Retry
	Progress                           func(report.Summary)
}

func Export(ctx context.Context, api API, o ExportOptions, s *report.Summary) (err error) {
	s.Phase = "preflight"
	if o.PageSize < 1 || !validRate(o.ReadRate) {
		return report.Fail(2, s.Phase, fmt.Errorf("page size must be positive; read rate must be nonnegative"))
	}
	if err = o.Retry.Validate(); err != nil {
		return report.Fail(2, s.Phase, err)
	}
	gz, err := Compression(o.Output, o.Compression)
	if err != nil {
		return report.Fail(2, s.Phase, err)
	}
	if o.Format != "json" && o.Format != "dynamodb-jsonl" {
		return report.Fail(2, s.Phase, fmt.Errorf("unsupported format"))
	}
	if _, err = os.Lstat(o.Output); err == nil && !o.Force {
		return report.Fail(4, s.Phase, fmt.Errorf("output exists; use --force"))
	} else if err != nil && !os.IsNotExist(err) {
		return report.Fail(4, s.Phase, err)
	}
	partial := o.Output + ".partial"
	f, err := report.NewFile(partial)
	if err != nil {
		return report.Fail(4, s.Phase, err)
	}
	s.Partial = partial
	defer func() {
		if f != nil {
			err = errors.Join(err, f.Close())
		}
	}()
	buf := bufio.NewWriterSize(f, 64*1024)
	var w io.Writer = buf
	var z *gzip.Writer
	if gz {
		z = gzip.NewWriter(buf)
		w = z
	}
	enc, err := format.NewEncoder(w, o.Format)
	if err != nil {
		return report.Fail(4, s.Phase, err)
	}
	s.Phase = "export"
	var cursor map[string]types.AttributeValue
	p := NewPacer(o.ReadRate)
	for {
		if err = p.Wait(ctx); err != nil {
			return err
		}
		out, e := retryCall(ctx, o.Retry, &s.Retries, func(c context.Context) (*dynamodb.ScanOutput, error) {
			return api.Scan(c, &dynamodb.ScanInput{TableName: aws.String(o.Table), ExclusiveStartKey: cursor, Limit: aws.Int32(o.PageSize), ConsistentRead: aws.Bool(o.Consistent), ReturnConsumedCapacity: types.ReturnConsumedCapacityTotal})
		})
		if e != nil {
			return report.Fail(3, s.Phase, e)
		}
		s.Scanned += int64(out.ScannedCount)
		if out.ConsumedCapacity != nil {
			s.Capacity += aws.ToFloat64(out.ConsumedCapacity.CapacityUnits)
		}
		for _, item := range out.Items {
			if e = ctx.Err(); e != nil {
				return e
			}
			if e = enc.Write(item); e != nil {
				return report.Fail(4, s.Phase, e)
			}
			s.Confirmed++
		}
		if o.Progress != nil {
			o.Progress(*s)
		}
		cursor = out.LastEvaluatedKey
		if len(cursor) == 0 {
			break
		}
	}
	s.Phase = "finalization"
	if err = enc.Close(); err != nil {
		return report.Fail(4, s.Phase, err)
	}
	if z != nil {
		if err = z.Close(); err != nil {
			return report.Fail(4, s.Phase, err)
		}
	}
	if err = buf.Flush(); err != nil {
		return report.Fail(4, s.Phase, err)
	}
	if err = f.Sync(); err != nil {
		return report.Fail(4, s.Phase, err)
	}
	st, err := f.Stat()
	if err != nil {
		return report.Fail(4, s.Phase, err)
	}
	s.Bytes = st.Size()
	if err = f.Close(); err != nil {
		f = nil
		return report.Fail(4, s.Phase, err)
	}
	f = nil
	// A hard link gives no-clobber publication on supported local filesystems.
	if !o.Force {
		if err = os.Link(partial, o.Output); err == nil {
			err = os.Remove(partial)
		}
	} else {
		err = os.Rename(partial, o.Output)
	}
	if err != nil {
		return report.Fail(4, s.Phase, err)
	}
	s.Partial = ""
	s.Phase = "complete"
	return nil
}
