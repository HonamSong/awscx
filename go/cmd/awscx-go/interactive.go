package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"

	awsx "github.com/HonamSong/awscx/go/internal/aws"
)

// stdin reader is shared across prompts so leftover buffer bytes carry over.
var stdin = bufio.NewReader(os.Stdin)

func promptLine(msg string) string {
	fmt.Print(msg)
	line, _ := stdin.ReadString('\n')
	return strings.TrimSpace(line)
}

// pickFromList prints a numbered list and reads a selection (index or name).
// Returns an error when the user cancels or the input is unrecognized.
func pickFromList(header string, items []string) (string, error) {
	if len(items) == 0 {
		return "", fmt.Errorf("항목이 없습니다")
	}
	fmt.Println()
	fmt.Println(header)
	for i, it := range items {
		fmt.Printf("  %2d) %s\n", i+1, it)
	}
	in := promptLine("\n번호 또는 이름 입력 (q=취소): ")
	if in == "" || in == "q" {
		return "", fmt.Errorf("취소되었습니다")
	}
	if n, err := strconv.Atoi(in); err == nil && n >= 1 && n <= len(items) {
		return items[n-1], nil
	}
	for _, it := range items {
		if it == in {
			return it, nil
		}
	}
	return "", fmt.Errorf("잘못된 선택: %s", in)
}

// runInteractive is the no-args entry: profile → mode → list.
// Stopgap until the bubbletea TUI is in place.
func runInteractive(ctx context.Context, profile, region string) error {
	if profile == "" {
		profs := awsx.AvailableProfiles()
		if len(profs) == 0 {
			return fmt.Errorf("사용 가능한 프로파일이 없습니다 (~/.aws/config 확인)")
		}
		p, err := pickFromList("프로파일 선택", profs)
		if err != nil {
			return err
		}
		profile = p
	}
	fmt.Printf("→ profile=%s region=%s\n", profile, region)

	clients, err := awsx.Load(ctx, profile, region)
	if err != nil {
		return fmt.Errorf("aws load: %w", err)
	}

	mode, err := pickFromList("접근 대상 선택", []string{
		"ECS (cluster/service)",
		"EC2 (SSM)",
	})
	if err != nil {
		return err
	}
	switch {
	case strings.HasPrefix(mode, "ECS"):
		return interactiveECS(ctx, clients)
	case strings.HasPrefix(mode, "EC2"):
		return dumpEC2(ctx, clients)
	}
	return nil
}

func interactiveECS(ctx context.Context, clients *awsx.Clients) error {
	cs, err := awsx.ListClusters(ctx, clients.ECS)
	if err != nil {
		return fmt.Errorf("list clusters: %w", err)
	}
	if len(cs) == 0 {
		fmt.Println("클러스터가 없습니다.")
		return nil
	}
	names := make([]string, len(cs))
	for i, c := range cs {
		names[i] = awssdk.ToString(c.ClusterName)
	}
	name, err := pickFromList("클러스터 선택", names)
	if err != nil {
		return err
	}
	svcs, err := awsx.ListServices(ctx, clients.ECS, name)
	if err != nil {
		return fmt.Errorf("list services: %w", err)
	}
	fmt.Printf("\n[%s] 서비스 %d개\n", name, len(svcs))
	for _, s := range svcs {
		fmt.Println("  ", s)
	}
	return nil
}

func dumpEC2(ctx context.Context, clients *awsx.Clients) error {
	insts, err := awsx.ListEC2Instances(ctx, clients.EC2, clients.SSM)
	if err != nil {
		return fmt.Errorf("list ec2: %w", err)
	}
	fmt.Printf("\nEC2 running 인스턴스 %d개\n", len(insts))
	for _, i := range insts {
		ssm := i.SSM
		if ssm == "" {
			ssm = "-"
		}
		fmt.Printf("  %-19s  %-20s  %-14s  %-15s  ssm=%s\n",
			i.ID, i.Name, i.Type, i.IP, ssm)
	}
	return nil
}