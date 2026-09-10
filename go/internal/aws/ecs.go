package aws

import (
	"context"
	"sort"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
)

// ClusterRow is a flat cluster summary for list rendering.
type ClusterRow struct {
	Name    string
	Status  string
	Active  int32
	Running int32
	Pending int32
}

// ServiceRow flattens per-service info shown on the services list.
// Zeroed fields (Min/Max nil, HasLB false) indicate absent data.
type ServiceRow struct {
	Name      string
	Status    string
	Running   int32
	Desired   int32
	Pending   int32
	ExecOn    bool
	HasLB     bool
	LBHealthy int
	LBTotal   int
	Min       *int32
	Max       *int32
}

// ListClusters paginates list_clusters + describe_clusters (batched by 100).
func ListClusters(ctx context.Context, c *ecs.Client) ([]ecstypes.Cluster, error) {
	var arns []string
	p := ecs.NewListClustersPaginator(c, &ecs.ListClustersInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		arns = append(arns, page.ClusterArns...)
	}
	var out []ecstypes.Cluster
	for i := 0; i < len(arns); i += 100 {
		end := i + 100
		if end > len(arns) {
			end = len(arns)
		}
		resp, err := c.DescribeClusters(ctx, &ecs.DescribeClustersInput{Clusters: arns[i:end]})
		if err != nil {
			return nil, err
		}
		out = append(out, resp.Clusters...)
	}
	sort.Slice(out, func(i, j int) bool {
		return awssdk.ToString(out[i].ClusterName) < awssdk.ToString(out[j].ClusterName)
	})
	return out, nil
}

// ListServices returns service short names for a cluster, sorted.
func ListServices(ctx context.Context, c *ecs.Client, clusterARN string) ([]string, error) {
	var arns []string
	p := ecs.NewListServicesPaginator(c, &ecs.ListServicesInput{Cluster: awssdk.String(clusterARN)})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		arns = append(arns, page.ServiceArns...)
	}
	names := make([]string, len(arns))
	for i, a := range arns {
		names[i] = ShortARN(a)
	}
	sort.Strings(names)
	return names, nil
}

// ListRunningTasks returns describe_tasks for RUNNING tasks in cluster,
// optionally scoped to a service.
func ListRunningTasks(ctx context.Context, c *ecs.Client, clusterARN, serviceName string) ([]ecstypes.Task, error) {
	in := &ecs.ListTasksInput{
		Cluster:       awssdk.String(clusterARN),
		DesiredStatus: ecstypes.DesiredStatus("RUNNING"),
	}
	if serviceName != "" {
		in.ServiceName = awssdk.String(serviceName)
	}
	var arns []string
	p := ecs.NewListTasksPaginator(c, in)
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		arns = append(arns, page.TaskArns...)
	}
	var out []ecstypes.Task
	for i := 0; i < len(arns); i += 100 {
		end := i + 100
		if end > len(arns) {
			end = len(arns)
		}
		resp, err := c.DescribeTasks(ctx, &ecs.DescribeTasksInput{
			Cluster: awssdk.String(clusterARN),
			Tasks:   arns[i:end],
		})
		if err != nil {
			return nil, err
		}
		out = append(out, resp.Tasks...)
	}
	return out, nil
}

// DescribeService returns a single service or nil if not found.
func DescribeService(ctx context.Context, c *ecs.Client, clusterARN, serviceName string) (*ecstypes.Service, error) {
	resp, err := c.DescribeServices(ctx, &ecs.DescribeServicesInput{
		Cluster:  awssdk.String(clusterARN),
		Services: []string{serviceName},
	})
	if err != nil {
		return nil, err
	}
	if len(resp.Services) == 0 {
		return nil, nil
	}
	return &resp.Services[0], nil
}

// DescribeServicesMap returns serviceName -> Service. Batches of 10
// (DescribeServices limit).
func DescribeServicesMap(ctx context.Context, c *ecs.Client, clusterARN string, names []string) (map[string]ecstypes.Service, error) {
	out := make(map[string]ecstypes.Service, len(names))
	for i := 0; i < len(names); i += 10 {
		end := i + 10
		if end > len(names) {
			end = len(names)
		}
		resp, err := c.DescribeServices(ctx, &ecs.DescribeServicesInput{
			Cluster:  awssdk.String(clusterARN),
			Services: names[i:end],
		})
		if err != nil {
			return nil, err
		}
		for _, s := range resp.Services {
			out[awssdk.ToString(s.ServiceName)] = s
		}
	}
	return out, nil
}

// GetTaskDefinition fetches a task definition by ARN or family:revision.
func GetTaskDefinition(ctx context.Context, c *ecs.Client, tdARN string) (*ecstypes.TaskDefinition, error) {
	resp, err := c.DescribeTaskDefinition(ctx, &ecs.DescribeTaskDefinitionInput{
		TaskDefinition: awssdk.String(tdARN),
	})
	if err != nil {
		return nil, err
	}
	return resp.TaskDefinition, nil
}

// AwsLogs mirrors the awslogs driver settings extracted from a container.
type AwsLogs struct {
	Group  string
	Region string
	Prefix string
}

// AwsLogsConfig extracts awslogs settings from a container definition.
// Returns nil when the container is not using the awslogs driver.
func AwsLogsConfig(cd ecstypes.ContainerDefinition) *AwsLogs {
	lc := cd.LogConfiguration
	if lc == nil || string(lc.LogDriver) != "awslogs" {
		return nil
	}
	return &AwsLogs{
		Group:  lc.Options["awslogs-group"],
		Region: lc.Options["awslogs-region"],
		Prefix: lc.Options["awslogs-stream-prefix"],
	}
}