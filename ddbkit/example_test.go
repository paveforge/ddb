package ddbkit_test

import (
	"context"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/paveforge/ddb/ddbkit"
	"time"
)

func ExampleLoad() {
	// Supply your configured client, including a local endpoint when appropriate.
	client := dynamodb.New(dynamodb.Options{Region: "us-east-1"})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	result, err := ddbkit.Load(ctx, client, ddbkit.Options{
		Table: "suite-users", TableFile: "testdata/table.json",
		Input: "testdata/users.jsonl", Truncate: true,
	})
	if err != nil {
		panic(err)
	}
	_ = result.Confirmed
}
