package aws

import (
	"context"
	"sort"
	"strings"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

// EC2Instance mirrors the flat dict returned by Python list_ec2_instances.
// SSM is the PingStatus ("Online", "ConnectionLost", ...); empty when the
// instance is not registered with SSM. OSUser is the AMI's default cloud
// login user (ec2-user, ubuntu, rocky, ...), populated by ResolveOSUsers.
type EC2Instance struct {
	ID       string
	Name     string
	Type     string
	State    string
	IP       string
	PublicIP string // EIP 또는 자동할당 퍼블릭 IP (없으면 "")
	SSM      string
	ImageId  string
	OSUser   string
}

// ListEC2Instances returns non-terminated instances augmented with SSM
// PingStatus. SSM lookup errors are swallowed so the base list remains usable.
func ListEC2Instances(ctx context.Context, e *ec2.Client, s *ssm.Client) ([]EC2Instance, error) {
	var out []EC2Instance
	p := ec2.NewDescribeInstancesPaginator(e, &ec2.DescribeInstancesInput{
		Filters: []ec2types.Filter{{
			Name:   awssdk.String("instance-state-name"),
			Values: []string{"pending", "running", "shutting-down", "stopping", "stopped"},
		}},
	})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, r := range page.Reservations {
			for _, i := range r.Instances {
				var name string
				for _, t := range i.Tags {
					if awssdk.ToString(t.Key) == "Name" {
						name = awssdk.ToString(t.Value)
						break
					}
				}
				state := ""
				if i.State != nil {
					state = string(i.State.Name)
				}
				out = append(out, EC2Instance{
					ID:       awssdk.ToString(i.InstanceId),
					Name:     name,
					Type:     string(i.InstanceType),
					State:    state,
					IP:       awssdk.ToString(i.PrivateIpAddress),
					PublicIP: awssdk.ToString(i.PublicIpAddress),
					ImageId:  awssdk.ToString(i.ImageId),
				})
			}
		}
	}

	online := map[string]string{}
	sp := ssm.NewDescribeInstanceInformationPaginator(s, &ssm.DescribeInstanceInformationInput{})
	for sp.HasMorePages() {
		page, err := sp.NextPage(ctx)
		if err != nil {
			break
		}
		for _, info := range page.InstanceInformationList {
			online[awssdk.ToString(info.InstanceId)] = string(info.PingStatus)
		}
	}
	for i := range out {
		out[i].SSM = online[out[i].ID]
	}

	sort.SliceStable(out, func(i, j int) bool {
		a := strings.ToLower(out[i].Name)
		if a == "" {
			a = out[i].ID
		}
		b := strings.ToLower(out[j].Name)
		if b == "" {
			b = out[j].ID
		}
		return a < b
	})
	return out, nil
}