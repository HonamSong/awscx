package aws

import (
	"context"
	"fmt"
	"strings"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/applicationautoscaling"
	aastypes "github.com/aws/aws-sdk-go-v2/service/applicationautoscaling/types"
)

const serviceNamespaceECS = aastypes.ServiceNamespace("ecs")

// AutoScaling mirrors Python service_autoscaling result.
type AutoScaling struct {
	Min      *int32
	Max      *int32
	Policies []aastypes.ScalingPolicy
}

// ServiceAutoScaling returns the AAS target + policies for one ECS service.
// Returns nil when there is no scalable target.
func ServiceAutoScaling(ctx context.Context, a *applicationautoscaling.Client, cluster, service string) (*AutoScaling, error) {
	rid := fmt.Sprintf("service/%s/%s", cluster, service)
	tgts, err := a.DescribeScalableTargets(ctx, &applicationautoscaling.DescribeScalableTargetsInput{
		ServiceNamespace: serviceNamespaceECS,
		ResourceIds:      []string{rid},
	})
	if err != nil {
		return nil, err
	}
	if len(tgts.ScalableTargets) == 0 {
		return nil, nil
	}
	t := tgts.ScalableTargets[0]
	pols, err := a.DescribeScalingPolicies(ctx, &applicationautoscaling.DescribeScalingPoliciesInput{
		ServiceNamespace: serviceNamespaceECS,
		ResourceId:       awssdk.String(rid),
	})
	if err != nil {
		return nil, err
	}
	return &AutoScaling{
		Min:      t.MinCapacity,
		Max:      t.MaxCapacity,
		Policies: pols.ScalingPolicies,
	}, nil
}

// MinMax is a (min, max) capacity pair.
type MinMax struct {
	Min *int32
	Max *int32
}

// ClusterAutoScalingMap returns serviceName -> (min, max) for the given cluster.
// Batches of 50 (DescribeScalableTargets ResourceIds limit).
func ClusterAutoScalingMap(ctx context.Context, a *applicationautoscaling.Client, cluster string, names []string) (map[string]MinMax, error) {
	ids := make([]string, len(names))
	for i, n := range names {
		ids[i] = fmt.Sprintf("service/%s/%s", cluster, n)
	}
	out := make(map[string]MinMax, len(names))
	for i := 0; i < len(ids); i += 50 {
		end := i + 50
		if end > len(ids) {
			end = len(ids)
		}
		resp, err := a.DescribeScalableTargets(ctx, &applicationautoscaling.DescribeScalableTargetsInput{
			ServiceNamespace: serviceNamespaceECS,
			ResourceIds:      ids[i:end],
		})
		if err != nil {
			return nil, err
		}
		for _, t := range resp.ScalableTargets {
			rid := awssdk.ToString(t.ResourceId)
			if idx := strings.LastIndex(rid, "/"); idx >= 0 {
				out[rid[idx+1:]] = MinMax{Min: t.MinCapacity, Max: t.MaxCapacity}
			}
		}
	}
	return out, nil
}