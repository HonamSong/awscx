package tui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"

	awsx "github.com/HonamSong/awscx/go/internal/aws"
)

// Async fetch messages.

type profilesLoadedMsg struct{ profiles []awsx.ProfileToken }
type clientsLoadedMsg struct{ clients *awsx.Clients }
type clustersLoadedMsg struct{ clusters []ecstypes.Cluster }
type servicesLoadedMsg struct{ services []awsx.ServiceRow }
type ec2LoadedMsg struct {
	instances []awsx.EC2Instance
}

// numBufClearMsg fires after a timeout to clear a stale digit-jump buffer.
// If buf still equals the current m.numBuf when handled, we reset.
type numBufClearMsg struct{ buf string }

type vpcsLoadedMsg struct{ rows []awsx.VPCRow }
type secretsLoadedMsg struct{ rows []awsx.SecretRow }
type r53ZonesLoadedMsg struct{ rows []awsx.R53ZoneRow }
type r53RecordsLoadedMsg struct{ rows []awsx.R53RecordRow }

func fetchVPCs(c *awsx.Clients) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), awsTimeout)
		defer cancel()
		rows, err := awsx.ListVPCs(ctx, c.EC2)
		if err != nil {
			return errMsg{err}
		}
		return vpcsLoadedMsg{rows: rows}
	}
}

func fetchSecrets(c *awsx.Clients) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), awsTimeout)
		defer cancel()
		rows, err := awsx.ListSecrets(ctx, c.Secrets)
		if err != nil {
			return errMsg{err}
		}
		return secretsLoadedMsg{rows: rows}
	}
}

func fetchR53Zones(c *awsx.Clients) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), awsTimeout)
		defer cancel()
		rows, err := awsx.ListHostedZones(ctx, c.Route53)
		if err != nil {
			return errMsg{err}
		}
		return r53ZonesLoadedMsg{rows: rows}
	}
}

func fetchR53Records(c *awsx.Clients, zoneID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), awsTimeout)
		defer cancel()
		rows, err := awsx.ListRecordSets(ctx, c.Route53, zoneID)
		if err != nil {
			return errMsg{err}
		}
		return r53RecordsLoadedMsg{rows: rows}
	}
}
type errMsg struct{ err error }

func (e errMsg) Error() string { return e.err.Error() }

const awsTimeout = 30 * time.Second

func fetchProfiles() tea.Cmd {
	return func() tea.Msg {
		return profilesLoadedMsg{profiles: awsx.ProfileTokens()}
	}
}

func loadClients(profile, region string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), awsTimeout)
		defer cancel()
		c, err := awsx.Load(ctx, profile, region)
		if err != nil {
			return errMsg{err}
		}
		return clientsLoadedMsg{clients: c}
	}
}

func fetchClusters(c *awsx.Clients) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), awsTimeout)
		defer cancel()
		cs, err := awsx.ListClusters(ctx, c.ECS)
		if err != nil {
			return errMsg{err}
		}
		return clustersLoadedMsg{clusters: cs}
	}
}

func fetchServices(c *awsx.Clients, clusterName string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), awsTimeout)
		defer cancel()
		rows, err := awsx.FetchServiceRows(ctx, c, clusterName)
		if err != nil {
			return errMsg{err}
		}
		return servicesLoadedMsg{services: rows}
	}
}

func fetchEC2(c *awsx.Clients) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), awsTimeout)
		defer cancel()
		insts, err := awsx.ListEC2Instances(ctx, c.EC2, c.SSM)
		if err != nil {
			return errMsg{err}
		}
		// AMI 이름/설명에서 OS 기본 사용자를 유추 (best-effort).
		awsx.ResolveOSUsers(ctx, c.EC2, insts)
		return ec2LoadedMsg{instances: insts}
	}
}