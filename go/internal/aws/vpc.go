package aws

import (
	"context"
	"sort"
	"strings"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// VPCRow is a flat VPC summary for list rendering.
type VPCRow struct {
	ID        string
	Name      string
	CIDR      string
	State     string
	IsDefault bool
}

// ListVPCs returns all VPCs the caller can describe. Default VPC bubbles to
// the top; the rest sort by Name (or ID when name tag is missing).
func ListVPCs(ctx context.Context, e *ec2.Client) ([]VPCRow, error) {
	var out []VPCRow
	p := ec2.NewDescribeVpcsPaginator(e, &ec2.DescribeVpcsInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, v := range page.Vpcs {
			var name string
			for _, t := range v.Tags {
				if awssdk.ToString(t.Key) == "Name" {
					name = awssdk.ToString(t.Value)
					break
				}
			}
			out = append(out, VPCRow{
				ID:        awssdk.ToString(v.VpcId),
				Name:      name,
				CIDR:      awssdk.ToString(v.CidrBlock),
				State:     string(v.State),
				IsDefault: awssdk.ToBool(v.IsDefault),
			})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].IsDefault != out[j].IsDefault {
			return out[i].IsDefault
		}
		ki := strings.ToLower(out[i].Name)
		if ki == "" {
			ki = out[i].ID
		}
		kj := strings.ToLower(out[j].Name)
		if kj == "" {
			kj = out[j].ID
		}
		return ki < kj
	})
	return out, nil
}

// SubnetRow is a flat subnet summary for VPC detail.
type SubnetRow struct {
	ID          string
	Name        string
	AZ          string
	CIDR        string
	Available   int32
	MapPublicIP bool
}

// ListSubnets returns subnets in the given VPC, sorted by AZ then CIDR.
func ListSubnets(ctx context.Context, c *ec2.Client, vpcID string) ([]SubnetRow, error) {
	var out []SubnetRow
	p := ec2.NewDescribeSubnetsPaginator(c, &ec2.DescribeSubnetsInput{
		Filters: []ec2types.Filter{{
			Name:   awssdk.String("vpc-id"),
			Values: []string{vpcID},
		}},
	})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, s := range page.Subnets {
			var name string
			for _, t := range s.Tags {
				if awssdk.ToString(t.Key) == "Name" {
					name = awssdk.ToString(t.Value)
					break
				}
			}
			row := SubnetRow{
				ID:          awssdk.ToString(s.SubnetId),
				Name:        name,
				AZ:          awssdk.ToString(s.AvailabilityZone),
				CIDR:        awssdk.ToString(s.CidrBlock),
				MapPublicIP: awssdk.ToBool(s.MapPublicIpOnLaunch),
			}
			if s.AvailableIpAddressCount != nil {
				row.Available = *s.AvailableIpAddressCount
			}
			out = append(out, row)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].AZ != out[j].AZ {
			return out[i].AZ < out[j].AZ
		}
		return out[i].CIDR < out[j].CIDR
	})
	return out, nil
}

// NatGWRow is a flat NAT gateway summary for VPC detail.
type NatGWRow struct {
	ID        string
	SubnetID  string
	State     string
	PublicIP  string
	PrivateIP string
}

// ListNatGateways returns NAT gateways in the given VPC. Populates the
// primary EIP + private IP from the first address entry.
func ListNatGateways(ctx context.Context, c *ec2.Client, vpcID string) ([]NatGWRow, error) {
	var out []NatGWRow
	p := ec2.NewDescribeNatGatewaysPaginator(c, &ec2.DescribeNatGatewaysInput{
		Filter: []ec2types.Filter{{
			Name:   awssdk.String("vpc-id"),
			Values: []string{vpcID},
		}},
	})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, n := range page.NatGateways {
			r := NatGWRow{
				ID:       awssdk.ToString(n.NatGatewayId),
				SubnetID: awssdk.ToString(n.SubnetId),
				State:    string(n.State),
			}
			for _, addr := range n.NatGatewayAddresses {
				if r.PublicIP == "" {
					r.PublicIP = awssdk.ToString(addr.PublicIp)
				}
				if r.PrivateIP == "" {
					r.PrivateIP = awssdk.ToString(addr.PrivateIp)
				}
			}
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}