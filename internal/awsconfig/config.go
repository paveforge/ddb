package awsconfig

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
)

type Options struct{ Profile, Region, Endpoint string }

func Load(ctx context.Context, o Options) (*dynamodb.Client, string, error) {
	if o.Endpoint != "" {
		u, err := url.Parse(o.Endpoint)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return nil, "", fmt.Errorf("endpoint must be an absolute HTTP(S) URL")
		}
	}
	opts := []func(*config.LoadOptions) error{config.WithRetryMaxAttempts(1), config.WithHTTPClient(&http.Client{Timeout: 30 * time.Second})}
	if o.Profile != "" {
		opts = append(opts, config.WithSharedConfigProfile(o.Profile))
	}
	if o.Region != "" {
		opts = append(opts, config.WithRegion(o.Region))
	}
	cfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, "", fmt.Errorf("load AWS configuration: %w", err)
	}
	if cfg.Region == "" {
		return nil, "", fmt.Errorf("AWS region is required; use --region or AWS configuration")
	}
	client := dynamodb.NewFromConfig(cfg, func(v *dynamodb.Options) {
		if o.Endpoint != "" {
			v.BaseEndpoint = aws.String(o.Endpoint)
		}
	})
	return client, cfg.Region, nil
}
