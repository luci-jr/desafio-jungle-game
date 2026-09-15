package messaging

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
)

// Config define os parâmetros de conexão ao AWS SQS / LocalStack.
type Config struct {
	Region    string
	Endpoint  string
	AccessKey string
	SecretKey string
	QueueURL  string
	DLQURL    string
}

// NewSQSClient inicializa o cliente SQS com suporte ao endpoint customizado do LocalStack.
func NewSQSClient(cfg Config) (*sqs.Client, error) {
	customResolver := aws.EndpointResolverWithOptionsFunc(
		func(service, region string, options ...interface{}) (aws.Endpoint, error) {
			if cfg.Endpoint != "" {
				return aws.Endpoint{
					PartitionID:       "aws",
					URL:               cfg.Endpoint,
					SigningRegion:     cfg.Region,
					HostnameImmutable: true,
				}, nil
			}
			return aws.Endpoint{}, &aws.EndpointNotFoundError{}
		},
	)

	awsCfg, err := config.LoadDefaultConfig(
		context.TODO(),
		config.WithRegion(cfg.Region),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(cfg.AccessKey, cfg.SecretKey, "")),
		config.WithEndpointResolverWithOptions(customResolver),
	)
	if err != nil {
		return nil, fmt.Errorf("falha ao carregar configuração AWS: %w", err)
	}

	return sqs.NewFromConfig(awsCfg), nil
}
