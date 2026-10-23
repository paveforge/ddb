package ddbkit

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/paveforge/ddb/internal/report"
	"github.com/paveforge/ddb/internal/table"
	"github.com/paveforge/ddb/internal/transfer"
)

// Client is the DynamoDB API needed by Load. *dynamodb.Client implements it.
type Client interface {
	Scan(context.Context, *dynamodb.ScanInput, ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error)
	BatchWriteItem(context.Context, *dynamodb.BatchWriteItemInput, ...func(*dynamodb.Options)) (*dynamodb.BatchWriteItemOutput, error)
	DescribeTable(context.Context, *dynamodb.DescribeTableInput, ...func(*dynamodb.Options)) (*dynamodb.DescribeTableOutput, error)
	CreateTable(context.Context, *dynamodb.CreateTableInput, ...func(*dynamodb.Options)) (*dynamodb.CreateTableOutput, error)
}

// Options configures a fixture load. Zero-valued tuning fields use defaults.
type Options struct {
	// Table names an existing table, or overrides TableFile's destination name.
	Table string
	// TableFile permits creation when missing and checks existing schema compatibility.
	TableFile string
	Input     string
	// Format defaults to "dynamodb-jsonl"; "json" is also supported.
	Format string
	// Compression defaults to "auto"; "none" and "gzip" are also supported.
	Compression string
	// Truncate deletes all existing records before inserting the validated fixture.
	Truncate      bool
	Concurrency   int           // Default: 4.
	WriteRate     float64       // Requests per second; zero means unlimited.
	RetryAttempts int           // Default: 8.
	RetryTimeout  time.Duration // Per request/batch retry budget. Default: 2 minutes.
	WaitTimeout   time.Duration // Table creation readiness budget. Default: 5 minutes.
	// Recovery is an optional new file path for unconfirmed records on failure.
	// By default Load uses Input + ".recovery.jsonl". Preserve it for diagnosis.
	Recovery string
}

// Result describes completed and partial work, including when Load returns an error.
type Result struct {
	Table                                                            string
	Phase                                                            string
	Created                                                          bool
	Validated, Confirmed, Deleted, Retries, Unresolved, NotAttempted int64
	// DeleteUnresolved counts deletes whose outcome could not be confirmed.
	DeleteUnresolved                     int64
	TruncateStarted, TruncateComplete    bool
	CreationAttempted, CreationUncertain bool
	Recovery                             string
	RecoveryComplete                     bool
}

// Load validates the fixture before mutation, creates a missing table when a
// definition is supplied, optionally truncates it, and waits for all writes to be
// acknowledged. Matching items are replaced completely, not merged. On failure,
// Result describes partial progress; completed mutations are not rolled back.
func Load(ctx context.Context, client Client, o Options) (Result, error) {
	if client == nil {
		return Result{}, fmt.Errorf("ddbkit: client is required")
	}
	if o.Input == "" {
		return Result{}, fmt.Errorf("ddbkit: input is required")
	}
	if o.Format == "" {
		o.Format = "dynamodb-jsonl"
	}
	if o.Compression == "" {
		o.Compression = "auto"
	}
	if o.Concurrency == 0 {
		o.Concurrency = 4
	}
	if o.RetryAttempts == 0 {
		o.RetryAttempts = 8
	}
	if o.RetryTimeout == 0 {
		o.RetryTimeout = 2 * time.Minute
	}
	if o.WaitTimeout == 0 {
		o.WaitTimeout = 5 * time.Minute
	}
	dest := o.Table
	if o.TableFile != "" {
		def, err := table.Load(o.TableFile)
		if err != nil {
			return Result{}, fmt.Errorf("ddbkit: %w", err)
		}
		if dest == "" {
			dest = *def.TableName
		}
	}
	opts := transfer.ImportOptions{Table: o.Table, TableFile: o.TableFile, Input: o.Input,
		Format: o.Format, Compression: o.Compression, Recovery: o.Recovery,
		Overwrite: true, Truncate: o.Truncate, Concurrency: o.Concurrency, WriteRate: o.WriteRate,
		Retry: transfer.Retry{Attempts: o.RetryAttempts, Timeout: o.RetryTimeout}, WaitTimeout: o.WaitTimeout}
	if o.TableFile != "" {
		opts.Table = ""
		opts.TableNameOverride = o.Table
	}
	if o.Truncate {
		opts.ConfirmTable = dest
	}
	s := &report.Summary{}
	err := transfer.Import(ctx, client, opts, s)
	return Result{Table: s.Table, Phase: s.Phase, Created: s.Created, Validated: s.Validated,
		Confirmed: s.Confirmed, Deleted: s.Deleted, DeleteUnresolved: s.DeleteUnresolved, Retries: s.Retries, Unresolved: s.Unresolved,
		NotAttempted: s.NotAttempted, TruncateStarted: s.TruncateStarted, TruncateComplete: s.TruncateComplete,
		CreationAttempted: s.CreationAttempted, CreationUncertain: s.CreationUncertain,
		Recovery: s.Recovery, RecoveryComplete: s.RecoveryComplete}, err
}
