package aws

import (
	"context"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
)

// FetchServiceRows returns enriched rows (status, task count, exec-on, LB
// health, autoscaling) for every service in a cluster. Mirrors what Python
// _load_targets builds. Best-effort: LB and autoscaling errors are swallowed
// so missing sub-permissions don't blank the whole list.
func FetchServiceRows(ctx context.Context, c *Clients, cluster string) ([]ServiceRow, error) {
	names, err := ListServices(ctx, c.ECS, cluster)
	if err != nil {
		return nil, err
	}
	if len(names) == 0 {
		return nil, nil
	}
	svcMap, err := DescribeServicesMap(ctx, c.ECS, cluster, names)
	if err != nil {
		return nil, err
	}
	scaleMap, _ := ClusterAutoScalingMap(ctx, c.AAS, cluster, names)

	rows := make([]ServiceRow, 0, len(names))
	for _, name := range names {
		row := ServiceRow{Name: name}
		if svc, ok := svcMap[name]; ok {
			row.Status = awssdk.ToString(svc.Status)
			row.Running = svc.RunningCount
			row.Desired = svc.DesiredCount
			row.Pending = svc.PendingCount
			row.ExecOn = svc.EnableExecuteCommand
			if len(svc.LoadBalancers) > 0 {
				row.HasLB = true
				if lb, err := ServiceLBHealth(ctx, c.ELB, svc); err == nil && lb != nil {
					row.LBHealthy = lb.Healthy
					row.LBTotal = lb.Total
				}
			}
		}
		if mm, ok := scaleMap[name]; ok {
			row.Min = mm.Min
			row.Max = mm.Max
		}
		rows = append(rows, row)
	}
	return rows, nil
}