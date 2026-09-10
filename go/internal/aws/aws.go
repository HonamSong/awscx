// Package aws wraps aws-sdk-go-v2 clients used by awscx
// (ECS, EC2, SSM, CloudWatch, Logs, ELBv2, Application Auto Scaling).
package aws

import (
	"context"
	"strings"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/applicationautoscaling"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	elbv2 "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

// Clients holds all AWS service clients used by awscx.
type Clients struct {
	Cfg     awssdk.Config
	ECS     *ecs.Client
	EC2     *ec2.Client
	SSM     *ssm.Client
	CW      *cloudwatch.Client
	Logs    *cloudwatchlogs.Client
	ELB     *elbv2.Client
	AAS     *applicationautoscaling.Client
	Secrets *secretsmanager.Client
	Route53 *route53.Client
}

// Load builds Clients for the given profile / region. Both may be empty to
// use the default chain (env, ~/.aws/config, SSO, etc.).
func Load(ctx context.Context, profile, region string) (*Clients, error) {
	var opts []func(*awscfg.LoadOptions) error
	if profile != "" {
		opts = append(opts, awscfg.WithSharedConfigProfile(profile))
	}
	if region != "" {
		opts = append(opts, awscfg.WithRegion(region))
	}
	cfg, err := awscfg.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, err
	}
	return &Clients{
		Cfg:     cfg,
		ECS:     ecs.NewFromConfig(cfg),
		EC2:     ec2.NewFromConfig(cfg),
		SSM:     ssm.NewFromConfig(cfg),
		CW:      cloudwatch.NewFromConfig(cfg),
		Logs:    cloudwatchlogs.NewFromConfig(cfg),
		ELB:     elbv2.NewFromConfig(cfg),
		AAS:     applicationautoscaling.NewFromConfig(cfg),
		Secrets: secretsmanager.NewFromConfig(cfg),
		Route53: route53.NewFromConfig(cfg),
	}, nil
}

// ShortARN returns the last "/"-delimited segment of an ARN.
func ShortARN(arn string) string {
	if arn == "" {
		return arn
	}
	if i := strings.LastIndex(arn, "/"); i >= 0 {
		return arn[i+1:]
	}
	return arn
}