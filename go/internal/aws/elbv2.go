package aws

import (
	"context"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	elbv2 "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
)

// LBHealth is the (healthy, total) target counter for an ECS service.
type LBHealth struct {
	Healthy int
	Total   int
}

// ServiceLBHealth aggregates health across all target groups attached to a
// service. Returns nil when the service has no load balancers.
func ServiceLBHealth(ctx context.Context, e *elbv2.Client, svc ecstypes.Service) (*LBHealth, error) {
	var tgArns []string
	for _, lb := range svc.LoadBalancers {
		if lb.TargetGroupArn != nil && *lb.TargetGroupArn != "" {
			tgArns = append(tgArns, *lb.TargetGroupArn)
		}
	}
	if len(tgArns) == 0 {
		return nil, nil
	}
	out := &LBHealth{}
	for _, tg := range tgArns {
		resp, err := e.DescribeTargetHealth(ctx, &elbv2.DescribeTargetHealthInput{
			TargetGroupArn: awssdk.String(tg),
		})
		if err != nil {
			return nil, err
		}
		out.Total += len(resp.TargetHealthDescriptions)
		for _, d := range resp.TargetHealthDescriptions {
			if d.TargetHealth != nil && string(d.TargetHealth.State) == "healthy" {
				out.Healthy++
			}
		}
	}
	return out, nil
}

// LBInfo is a load balancer summary shown alongside target-group health.
type LBInfo struct {
	Name  string
	State string
	DNS   string
}

// TGHealth is the target-group health breakdown + associated LB info.
type TGHealth struct {
	Counts map[string]int
	LB     *LBInfo
}

// TargetGroupHealth returns per-state counts + the LB attached to the TG.
func TargetGroupHealth(ctx context.Context, e *elbv2.Client, tgARN string) (*TGHealth, error) {
	th, err := e.DescribeTargetHealth(ctx, &elbv2.DescribeTargetHealthInput{
		TargetGroupArn: awssdk.String(tgARN),
	})
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	for _, d := range th.TargetHealthDescriptions {
		if d.TargetHealth == nil {
			continue
		}
		counts[string(d.TargetHealth.State)]++
	}
	out := &TGHealth{Counts: counts}

	tgs, err := e.DescribeTargetGroups(ctx, &elbv2.DescribeTargetGroupsInput{
		TargetGroupArns: []string{tgARN},
	})
	if err != nil {
		return out, err
	}
	if len(tgs.TargetGroups) == 0 || len(tgs.TargetGroups[0].LoadBalancerArns) == 0 {
		return out, nil
	}
	lbs, err := e.DescribeLoadBalancers(ctx, &elbv2.DescribeLoadBalancersInput{
		LoadBalancerArns: tgs.TargetGroups[0].LoadBalancerArns,
	})
	if err != nil {
		return out, err
	}
	if len(lbs.LoadBalancers) > 0 {
		lb := lbs.LoadBalancers[0]
		state := "-"
		if lb.State != nil {
			state = string(lb.State.Code)
		}
		out.LB = &LBInfo{
			Name:  awssdk.ToString(lb.LoadBalancerName),
			State: state,
			DNS:   awssdk.ToString(lb.DNSName),
		}
	}
	return out, nil
}