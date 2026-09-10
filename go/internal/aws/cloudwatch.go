package aws

import (
	"context"
	"sort"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
)

// MetricSummary mirrors Python service_metric result.
type MetricSummary struct {
	Avg  float64
	Peak float64
}

func serviceDims(cluster, service string) []cwtypes.Dimension {
	return []cwtypes.Dimension{
		{Name: awssdk.String("ClusterName"), Value: awssdk.String(cluster)},
		{Name: awssdk.String("ServiceName"), Value: awssdk.String(service)},
	}
}

// ServiceMetric returns average / peak over the last hour for an AWS/ECS
// service metric. Returns nil when there are no datapoints.
func ServiceMetric(ctx context.Context, cw *cloudwatch.Client, metric, cluster, service string) (*MetricSummary, error) {
	now := time.Now().UTC()
	resp, err := cw.GetMetricStatistics(ctx, &cloudwatch.GetMetricStatisticsInput{
		Namespace:  awssdk.String("AWS/ECS"),
		MetricName: awssdk.String(metric),
		Dimensions: serviceDims(cluster, service),
		StartTime:  awssdk.Time(now.Add(-time.Hour)),
		EndTime:    awssdk.Time(now),
		Period:     awssdk.Int32(300),
		Statistics: []cwtypes.Statistic{
			cwtypes.Statistic("Average"),
			cwtypes.Statistic("Maximum"),
		},
	})
	if err != nil {
		return nil, err
	}
	if len(resp.Datapoints) == 0 {
		return nil, nil
	}
	var sum, peak float64
	for _, d := range resp.Datapoints {
		if d.Average != nil {
			sum += *d.Average
		}
		if d.Maximum != nil && *d.Maximum > peak {
			peak = *d.Maximum
		}
	}
	return &MetricSummary{Avg: sum / float64(len(resp.Datapoints)), Peak: peak}, nil
}

// ServiceMetricSeries returns AWS/ECS Average datapoints in time order.
func ServiceMetricSeries(ctx context.Context, cw *cloudwatch.Client, metric, cluster, service string, period int32, hours int) ([]float64, error) {
	return metricSeries(ctx, cw, "AWS/ECS", metric, cluster, service, "Average", period, hours)
}

// InsightsMetricSeries returns ECS/ContainerInsights datapoints in time order.
// stat defaults to "Average" when empty.
func InsightsMetricSeries(ctx context.Context, cw *cloudwatch.Client, metric, cluster, service, stat string, period int32, hours int) ([]float64, error) {
	if stat == "" {
		stat = "Average"
	}
	return metricSeries(ctx, cw, "ECS/ContainerInsights", metric, cluster, service, stat, period, hours)
}

func metricSeries(ctx context.Context, cw *cloudwatch.Client, namespace, metric, cluster, service, stat string, period int32, hours int) ([]float64, error) {
	if period == 0 {
		period = 60
	}
	if hours == 0 {
		hours = 1
	}
	now := time.Now().UTC()
	resp, err := cw.GetMetricStatistics(ctx, &cloudwatch.GetMetricStatisticsInput{
		Namespace:  awssdk.String(namespace),
		MetricName: awssdk.String(metric),
		Dimensions: serviceDims(cluster, service),
		StartTime:  awssdk.Time(now.Add(-time.Duration(hours) * time.Hour)),
		EndTime:    awssdk.Time(now),
		Period:     awssdk.Int32(period),
		Statistics: []cwtypes.Statistic{cwtypes.Statistic(stat)},
	})
	if err != nil {
		return nil, err
	}
	dps := resp.Datapoints
	sort.Slice(dps, func(i, j int) bool {
		return dps[i].Timestamp.Before(*dps[j].Timestamp)
	})
	out := make([]float64, 0, len(dps))
	for _, d := range dps {
		if v := pickStat(d, stat); v != nil {
			out = append(out, *v)
		}
	}
	return out, nil
}

func pickStat(d cwtypes.Datapoint, stat string) *float64 {
	switch stat {
	case "Average":
		return d.Average
	case "Sum":
		return d.Sum
	case "Minimum":
		return d.Minimum
	case "Maximum":
		return d.Maximum
	case "SampleCount":
		return d.SampleCount
	}
	return nil
}