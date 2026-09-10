package aws

import (
	"context"
	"strings"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
)

// InferOSUser guesses the OS default cloud login user from an AMI's name +
// description. Returns "" when the pattern doesn't match any known distro.
func InferOSUser(imageName, imageDesc string) string {
	s := strings.ToLower(imageName + " " + imageDesc)
	switch {
	case strings.Contains(s, "ubuntu"):
		return "ubuntu"
	case strings.Contains(s, "rocky"):
		return "rocky"
	case strings.Contains(s, "almalinux"), strings.Contains(s, "alma-linux"):
		return "ec2-user"
	case strings.Contains(s, "centos"):
		return "centos"
	case strings.Contains(s, "debian"):
		return "admin"
	case strings.Contains(s, "fedora"):
		return "fedora"
	case strings.Contains(s, "rhel"), strings.Contains(s, "red hat"), strings.Contains(s, "red-hat"):
		return "ec2-user"
	case strings.Contains(s, "suse"), strings.Contains(s, "sles"):
		return "ec2-user"
	case strings.Contains(s, "oracle linux"), strings.Contains(s, "oracle-linux"):
		return "ec2-user"
	case strings.Contains(s, "bitnami"):
		return "bitnami"
	case strings.Contains(s, "windows"):
		return "Administrator"
	// Amazon Linux 계열 (al2023, amzn2, amzn-ami 등)은 가장 마지막에.
	case strings.Contains(s, "amzn"),
		strings.Contains(s, "amazon linux"),
		strings.Contains(s, "amazon-linux"),
		strings.Contains(s, "al2023"),
		strings.Contains(s, "al2 "),
		strings.HasPrefix(s, "al2"):
		return "ec2-user"
	}
	return ""
}

// ResolveOSUsers batches DescribeImages for the unique ImageIds on the given
// instances and populates OSUser in-place. Silent on permission / lookup
// errors — worst case the column shows "-".
func ResolveOSUsers(ctx context.Context, c *ec2.Client, insts []EC2Instance) {
	idSet := map[string]struct{}{}
	for _, i := range insts {
		if i.ImageId != "" {
			idSet[i.ImageId] = struct{}{}
		}
	}
	if len(idSet) == 0 {
		return
	}
	ids := make([]string, 0, len(idSet))
	for id := range idSet {
		ids = append(ids, id)
	}
	userByImage := map[string]string{}
	// DescribeImages accepts many ImageIds; batch conservatively.
	for i := 0; i < len(ids); i += 100 {
		end := i + 100
		if end > len(ids) {
			end = len(ids)
		}
		resp, err := c.DescribeImages(ctx, &ec2.DescribeImagesInput{
			ImageIds: ids[i:end],
		})
		if err != nil {
			continue
		}
		for _, img := range resp.Images {
			name := awssdk.ToString(img.Name)
			desc := awssdk.ToString(img.Description)
			if u := InferOSUser(name, desc); u != "" {
				userByImage[awssdk.ToString(img.ImageId)] = u
			}
		}
	}
	for i := range insts {
		insts[i].OSUser = userByImage[insts[i].ImageId]
	}
}