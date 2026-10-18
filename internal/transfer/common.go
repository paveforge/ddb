package transfer

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/aws/smithy-go"
)

type Retry struct {
	Attempts int
	Timeout  time.Duration
}

func (r Retry) Validate() error {
	if r.Attempts < 1 || r.Attempts > 100 || r.Timeout <= 0 {
		return fmt.Errorf("retry attempts must be 1..100 and timeout positive")
	}
	return nil
}
func transient(err error) bool {
	var a smithy.APIError
	if errors.As(err, &a) {
		switch a.ErrorCode() {
		case "ProvisionedThroughputExceededException", "ThrottlingException", "RequestLimitExceeded", "InternalServerError", "ServiceUnavailable", "RequestTimeout", "ReplicatedWriteConflictException":
			return true
		}
		return a.ErrorFault() == smithy.FaultServer
	}
	var n net.Error
	return errors.As(err, &n) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
}
func pause(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return ctx.Err()
	}
}
func backoff(ctx context.Context, attempt int) error {
	shift := min(attempt, 6)
	return pause(ctx, time.Duration(rand.Int64N(int64(100*time.Millisecond*(1<<shift))+1)))
}
func retryCall[T any](ctx context.Context, r Retry, retries *int64, call func(context.Context) (T, error)) (T, error) {
	ctx, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel()
	var v T
	var err error
	for i := 0; i < r.Attempts; i++ {
		if err = ctx.Err(); err != nil {
			return v, err
		}
		v, err = call(ctx)
		if err == nil || !transient(err) {
			return v, err
		}
		if i+1 < r.Attempts {
			*retries++
			if err = backoff(ctx, i); err != nil {
				return v, err
			}
		}
	}
	return v, err
}

type Pacer struct {
	mu       sync.Mutex
	next     time.Time
	interval time.Duration
}

func validRate(rate float64) bool { return rate >= 0 && !math.IsNaN(rate) && !math.IsInf(rate, 0) }
func NewPacer(rate float64) *Pacer {
	p := &Pacer{}
	if rate > 0 {
		p.interval = time.Duration(float64(time.Second) / rate)
	}
	return p
}
func (p *Pacer) Wait(ctx context.Context) error {
	p.mu.Lock()
	now := time.Now()
	at := p.next
	if at.Before(now) {
		at = now
	}
	p.next = at.Add(p.interval)
	p.mu.Unlock()
	return pause(ctx, time.Until(at))
}
func Compression(path, mode string) (bool, error) {
	switch mode {
	case "", "auto":
		return strings.HasSuffix(strings.ToLower(path), ".gz"), nil
	case "gzip":
		return true, nil
	case "none":
		return false, nil
	}
	return false, fmt.Errorf("compression must be auto, gzip, or none")
}
func Input(path, compression string) (io.ReadCloser, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() {
		f.Close()
		return nil, fmt.Errorf("input must be a regular file")
	}
	gz, e := Compression(path, compression)
	if e != nil {
		f.Close()
		return nil, e
	}
	if !gz {
		return f, nil
	}
	z, e := gzip.NewReader(f)
	if e != nil {
		f.Close()
		return nil, e
	}
	return &inputReader{Reader: z, z: z, f: f}, nil
}

type inputReader struct {
	io.Reader
	z *gzip.Reader
	f *os.File
}

func (r *inputReader) Close() error { return errors.Join(r.z.Close(), r.f.Close()) }
