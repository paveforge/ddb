package awsconfig

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
)

type Options struct{ Profile, Region, Endpoint, EnvFile string }

func Load(ctx context.Context, o Options) (*dynamodb.Client, string, error) {
	if o.Endpoint != "" {
		u, err := url.Parse(o.Endpoint)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return nil, "", fmt.Errorf("endpoint must be an absolute HTTP(S) URL")
		}
	}
	if o.EnvFile != "" && o.Profile != "" {
		return nil, "", fmt.Errorf("--env-file and --profile cannot be combined")
	}
	var values map[string]string
	if o.EnvFile != "" {
		var err error
		values, err = readEnvFile(o.EnvFile)
		if err != nil {
			return nil, "", err
		}
	}
	opts := []func(*config.LoadOptions) error{config.WithRetryMaxAttempts(1), config.WithHTTPClient(&http.Client{Timeout: 30 * time.Second})}
	if o.EnvFile != "" {
		access := setting(values, "AWS_ACCESS_KEY_ID", "AWS_ACCESS_KEY")
		secret := setting(values, "AWS_SECRET_ACCESS_KEY", "AWS_SECRET_KEY")
		token := setting(values, "AWS_SESSION_TOKEN")
		if (access == "") != (secret == "") {
			return nil, "", fmt.Errorf("AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY must be supplied together")
		}
		if token != "" && access == "" {
			return nil, "", fmt.Errorf("AWS_SESSION_TOKEN requires an access key and secret key")
		}
		if access != "" {
			opts = append(opts, config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(access, secret, token)))
		}
		if region := setting(values, "AWS_REGION", "AWS_DEFAULT_REGION"); region != "" {
			opts = append(opts, config.WithRegion(region))
		}
	}
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
