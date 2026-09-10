package aws

import (
	"context"
	"encoding/base64"
	"fmt"
	"sort"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
)

// SecretRow is a flat Secrets Manager summary for list rendering.
// Sensitive value fields (SecretString/SecretBinary) are never fetched.
type SecretRow struct {
	Name            string
	Description     string
	KMSKeyID        string
	LastChanged     time.Time
	LastRotated     time.Time
	RotationEnabled bool
}

// ListSecrets paginates all secret metadata. No secret VALUES are retrieved.
func ListSecrets(ctx context.Context, c *secretsmanager.Client) ([]SecretRow, error) {
	var out []SecretRow
	p := secretsmanager.NewListSecretsPaginator(c, &secretsmanager.ListSecretsInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, s := range page.SecretList {
			row := SecretRow{
				Name:            awssdk.ToString(s.Name),
				Description:     awssdk.ToString(s.Description),
				KMSKeyID:        awssdk.ToString(s.KmsKeyId),
				RotationEnabled: s.RotationEnabled != nil && *s.RotationEnabled,
			}
			if s.LastChangedDate != nil {
				row.LastChanged = *s.LastChangedDate
			}
			if s.LastRotatedDate != nil {
				row.LastRotated = *s.LastRotatedDate
			}
			out = append(out, row)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// SecretTag is a simple key/value pair.
type SecretTag struct{ Key, Value string }

// SecretDetail is the expanded metadata for one secret (DescribeSecret).
// Value is fetched separately by GetSecretValue.
type SecretDetail struct {
	Name             string
	ARN              string
	Description      string
	KMSKeyID         string
	RotationEnabled  bool
	RotationRules    string
	CreatedDate      time.Time
	LastChangedDate  time.Time
	LastRotatedDate  time.Time
	NextRotationDate time.Time
	Tags             []SecretTag
	Versions         map[string][]string // versionId → stage labels
}

// DescribeSecret returns metadata (no value).
func DescribeSecret(ctx context.Context, c *secretsmanager.Client, id string) (*SecretDetail, error) {
	resp, err := c.DescribeSecret(ctx, &secretsmanager.DescribeSecretInput{
		SecretId: awssdk.String(id),
	})
	if err != nil {
		return nil, err
	}
	d := &SecretDetail{
		Name:            awssdk.ToString(resp.Name),
		ARN:             awssdk.ToString(resp.ARN),
		Description:     awssdk.ToString(resp.Description),
		KMSKeyID:        awssdk.ToString(resp.KmsKeyId),
		RotationEnabled: resp.RotationEnabled != nil && *resp.RotationEnabled,
	}
	if resp.CreatedDate != nil {
		d.CreatedDate = *resp.CreatedDate
	}
	if resp.LastChangedDate != nil {
		d.LastChangedDate = *resp.LastChangedDate
	}
	if resp.LastRotatedDate != nil {
		d.LastRotatedDate = *resp.LastRotatedDate
	}
	if resp.NextRotationDate != nil {
		d.NextRotationDate = *resp.NextRotationDate
	}
	if resp.RotationRules != nil && resp.RotationRules.AutomaticallyAfterDays != nil {
		d.RotationRules = fmt.Sprintf("every %d days", *resp.RotationRules.AutomaticallyAfterDays)
	}
	for _, t := range resp.Tags {
		d.Tags = append(d.Tags, SecretTag{
			Key:   awssdk.ToString(t.Key),
			Value: awssdk.ToString(t.Value),
		})
	}
	d.Versions = map[string][]string{}
	for id, stages := range resp.VersionIdsToStages {
		d.Versions[id] = stages
	}
	return d, nil
}

// GetSecretValue fetches the value. Binary secrets are base64-encoded.
// CALLER MUST TREAT RETURN VALUE AS SENSITIVE — do not log/echo.
func GetSecretValue(ctx context.Context, c *secretsmanager.Client, id string) (string, error) {
	resp, err := c.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
		SecretId: awssdk.String(id),
	})
	if err != nil {
		return "", err
	}
	if resp.SecretString != nil {
		return *resp.SecretString, nil
	}
	if len(resp.SecretBinary) > 0 {
		return base64.StdEncoding.EncodeToString(resp.SecretBinary), nil
	}
	return "", nil
}