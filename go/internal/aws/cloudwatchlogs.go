package aws

import (
	"context"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
)

// LogEvent mirrors the fields we care about from CloudWatch Logs FilterLogEvents.
type LogEvent struct {
	Timestamp int64 // millis since epoch
	Message   string
	Stream    string
}

// FilterLogEvents fetches events from the given log group since startMs.
// streamPrefix is optional (empty = no filter). limit ≤ 10000.
func FilterLogEvents(ctx context.Context, c *cloudwatchlogs.Client, group, streamPrefix string, startMs int64, limit int32) ([]LogEvent, error) {
	in := &cloudwatchlogs.FilterLogEventsInput{
		LogGroupName: awssdk.String(group),
		StartTime:    awssdk.Int64(startMs),
		Limit:        awssdk.Int32(limit),
	}
	if streamPrefix != "" {
		in.LogStreamNamePrefix = awssdk.String(streamPrefix)
	}
	resp, err := c.FilterLogEvents(ctx, in)
	if err != nil {
		return nil, err
	}
	out := make([]LogEvent, 0, len(resp.Events))
	for _, e := range resp.Events {
		ev := LogEvent{}
		if e.Timestamp != nil {
			ev.Timestamp = *e.Timestamp
		}
		if e.Message != nil {
			ev.Message = *e.Message
		}
		if e.LogStreamName != nil {
			ev.Stream = *e.LogStreamName
		}
		out = append(out, ev)
	}
	return out, nil
}