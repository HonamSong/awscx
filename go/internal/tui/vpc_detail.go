package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	awsx "github.com/HonamSong/awscx/go/internal/aws"
)

type vpcDetailLoadedMsg struct {
	vpcID   string
	vpcName string
	cidr    string
	subnets []awsx.SubnetRow
	natgws  []awsx.NatGWRow
}

func fetchVPCDetail(c *awsx.Clients, v awsx.VPCRow) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), awsTimeout)
		defer cancel()
		subs, err := awsx.ListSubnets(ctx, c.EC2, v.ID)
		if err != nil {
			return errMsg{fmt.Errorf("list subnets: %w", err)}
		}
		// NAT GW 조회 실패는 조용히 무시 (권한 부족 등)
		nats, _ := awsx.ListNatGateways(ctx, c.EC2, v.ID)
		return vpcDetailLoadedMsg{
			vpcID:   v.ID,
			vpcName: v.Name,
			cidr:    v.CIDR,
			subnets: subs,
			natgws:  nats,
		}
	}
}

// renderVPCDetail formats a header + NAT gateways section + subnets section.
func renderVPCDetail(vpcID, vpcName, cidr string, subs []awsx.SubnetRow, nats []awsx.NatGWRow) string {
	var b strings.Builder

	b.WriteString(dimStyle.Render("VPC     : "))
	b.WriteString(vpcID)
	if vpcName != "" {
		b.WriteString("  " + boldStyle.Render(vpcName))
	}
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("CIDR    : "))
	b.WriteString(cyanStyle.Render(cidr) + "\n")

	// NAT Gateways
	b.WriteString("\n" + sectStyle.Render(fmt.Sprintf("NAT Gateways (%d):", len(nats))) + "\n")
	if len(nats) == 0 {
		b.WriteString(dimStyle.Render("  없음\n"))
	} else {
		for _, n := range nats {
			b.WriteString("  - " + n.ID + "  ")
			b.WriteString(dimStyle.Render("subnet="))
			b.WriteString(n.SubnetID + "  ")
			b.WriteString(dimStyle.Render("state="))
			b.WriteString(statusStyle(strings.ToUpper(n.State)).Render(n.State) + "\n")
			b.WriteString("      ")
			b.WriteString(dimStyle.Render("Public IP: "))
			if n.PublicIP == "" {
				b.WriteString(dimStyle.Render("-"))
			} else {
				b.WriteString(statusStyle("HEALTHY").Render(n.PublicIP))
			}
			b.WriteString("   ")
			b.WriteString(dimStyle.Render("Private IP: "))
			if n.PrivateIP == "" {
				b.WriteString(dimStyle.Render("-"))
			} else {
				b.WriteString(n.PrivateIP)
			}
			b.WriteString("\n")
		}
	}

	// Subnets
	b.WriteString("\n" + sectStyle.Render(fmt.Sprintf("Subnets (%d):", len(subs))) + "\n")
	if len(subs) == 0 {
		b.WriteString(dimStyle.Render("  없음\n"))
	} else {
		for _, s := range subs {
			typ := "private"
			typStyle := statusStyle("PENDING")
			if s.MapPublicIP {
				typ = "public"
				typStyle = statusStyle("HEALTHY")
			}
			b.WriteString(fmt.Sprintf("  - %-24s  ", s.ID))
			if s.Name != "" {
				b.WriteString(boldStyle.Render(s.Name) + "  ")
			}
			b.WriteString(dimStyle.Render(s.AZ + "  "))
			b.WriteString(cyanStyle.Render(s.CIDR) + "  ")
			b.WriteString(typStyle.Render(typ) + "  ")
			b.WriteString(dimStyle.Render(fmt.Sprintf("(%d IPs free)", s.Available)) + "\n")
		}
	}
	return b.String()
}