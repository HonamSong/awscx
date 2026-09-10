package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"

	awsx "github.com/HonamSong/awscx/go/internal/aws"
	"github.com/HonamSong/awscx/go/internal/config"
	"github.com/HonamSong/awscx/go/internal/tui"
)

var version = "0.0.1"

const usage = `awscx-go — Python awscx 의 Go 포팅 (MVP TUI)

Usage:
  awscx-go [-p profile] [-r region]              bubbletea TUI 실행
  awscx-go [-p profile] [-r region] --no-tui     stdin 프롬프트 대화형 (fallback)
  awscx-go [-p profile] [-r region] <subcommand>

Subcommands (스크립트 용도):
  clusters              list ECS clusters
  services <cluster>    list ECS services in cluster
  ec2                   list running EC2 instances (+SSM ping)
`

func main() {
	var (
		profile     string
		region      string
		showVersion bool
		noTUI       bool
	)
	flag.StringVar(&profile, "p", "", "AWS profile name")
	flag.StringVar(&region, "r", "", "AWS region override")
	flag.BoolVar(&showVersion, "version", false, "print version and exit")
	flag.BoolVar(&noTUI, "no-tui", false, "fall back to stdin prompt instead of the bubbletea TUI")
	flag.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	flag.Parse()

	if showVersion {
		fmt.Println("awscx-go", version)
		return
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "config load:", err)
	}
	if region != "" {
		cfg.Region = region
	}
	config.SetupLogging(cfg, version)
	config.Logger.Debug("startup", "profile", profile, "region", cfg.Region, "cmd", flag.Args())

	args := flag.Args()

	if len(args) == 0 {
		if noTUI {
			if err := runInteractive(context.Background(), profile, cfg.Region); err != nil {
				die("%v", err)
			}
			return
		}
		if err := tui.Run(version, profile, cfg); err != nil {
			die("tui: %v", err)
		}
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	clients, err := awsx.Load(ctx, profile, cfg.Region)
	if err != nil {
		die("aws load: %v", err)
	}

	switch args[0] {
	case "clusters":
		cs, err := awsx.ListClusters(ctx, clients.ECS)
		if err != nil {
			die("list clusters: %v", err)
		}
		for _, c := range cs {
			fmt.Printf("%-40s  %s  running=%d  pending=%d\n",
				awssdk.ToString(c.ClusterName),
				awssdk.ToString(c.Status),
				c.RunningTasksCount, c.PendingTasksCount)
		}

	case "services":
		if len(args) < 2 {
			die("usage: services <cluster>")
		}
		names, err := awsx.ListServices(ctx, clients.ECS, args[1])
		if err != nil {
			die("list services: %v", err)
		}
		for _, n := range names {
			fmt.Println(n)
		}

	case "ec2":
		insts, err := awsx.ListEC2Instances(ctx, clients.EC2, clients.SSM)
		if err != nil {
			die("list ec2: %v", err)
		}
		for _, i := range insts {
			ssm := i.SSM
			if ssm == "" {
				ssm = "-"
			}
			fmt.Printf("%-19s  %-20s  %-14s  %-10s  %-15s  ssm=%s\n",
				i.ID, i.Name, i.Type, i.State, i.IP, ssm)
		}

	default:
		fmt.Fprintln(os.Stderr, "unknown subcommand:", args[0])
		flag.Usage()
		os.Exit(2)
	}
}

func die(format string, a ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", a...)
	os.Exit(1)
}