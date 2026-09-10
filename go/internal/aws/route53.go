package aws

import (
	"context"
	"sort"
	"strings"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/route53"
)

// R53ZoneRow is a flat Route53 hosted zone summary.
type R53ZoneRow struct {
	ID          string // "Z1234ABCD..." — /hostedzone/ prefix trimmed
	Name        string // "example.com."
	Private     bool
	RecordCount int64
	Comment     string
}

// ListHostedZones paginates all zones the caller can list.
func ListHostedZones(ctx context.Context, c *route53.Client) ([]R53ZoneRow, error) {
	var out []R53ZoneRow
	p := route53.NewListHostedZonesPaginator(c, &route53.ListHostedZonesInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, z := range page.HostedZones {
			row := R53ZoneRow{
				ID:   strings.TrimPrefix(awssdk.ToString(z.Id), "/hostedzone/"),
				Name: awssdk.ToString(z.Name),
			}
			if z.Config != nil {
				row.Private = z.Config.PrivateZone
				row.Comment = awssdk.ToString(z.Config.Comment)
			}
			if z.ResourceRecordSetCount != nil {
				row.RecordCount = *z.ResourceRecordSetCount
			}
			out = append(out, row)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// R53RecordRow is a flat resource record set summary.
// Alias records have empty TTL and populated AliasDNS instead of Values.
type R53RecordRow struct {
	Name     string
	Type     string
	TTL      int64
	Values   []string
	AliasDNS string
}

// ListRecordSets paginates all record sets in a zone.
func ListRecordSets(ctx context.Context, c *route53.Client, zoneID string) ([]R53RecordRow, error) {
	var out []R53RecordRow
	p := route53.NewListResourceRecordSetsPaginator(c, &route53.ListResourceRecordSetsInput{
		HostedZoneId: awssdk.String(zoneID),
	})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, r := range page.ResourceRecordSets {
			row := R53RecordRow{
				Name: awssdk.ToString(r.Name),
				Type: string(r.Type),
			}
			if r.TTL != nil {
				row.TTL = *r.TTL
			}
			for _, rr := range r.ResourceRecords {
				row.Values = append(row.Values, awssdk.ToString(rr.Value))
			}
			if r.AliasTarget != nil {
				row.AliasDNS = awssdk.ToString(r.AliasTarget.DNSName)
			}
			out = append(out, row)
		}
	}
	return out, nil
}