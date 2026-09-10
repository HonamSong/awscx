# awscx-go

![go](https://img.shields.io/badge/go-1.24%2B-blue.svg)
![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)
![platform](https://img.shields.io/badge/platform-macOS%20%7C%20Linux-lightgrey.svg)

Python 구현(../python)의 Go 포팅. `bubbletea + lipgloss` 기반 k9s 스타일 TUI로,
폐쇄망·오프라인 환경에 **단일 정적 바이너리** 하나로 배포하는 것을 주 목적으로 합니다.

Python 판(`awscx`)과 실행 파일 이름이 충돌하지 않도록 **`awscx-go`** 로 배포합니다.

## 주요 기능

### 프로파일 관리
- `~/.aws/config` + `~/.aws/credentials` 파싱 → 유형 자동 분류
  - `static` / `sso` / `assume-role` / `sts`(aws_session_token 직접 기록) / `external`(credential_process)
- **TOKEN 컬럼**: 캐시된 자격의 만료까지 남은 시간 (초록 = 여유, 노랑 = <15분, 빨강 = `EXPIRED`)
  - SSO: `~/.aws/sso/cache/<sha1(session|url)>.json`
  - assume-role: `~/.aws/cli/cache/*.json` 을 role-ARN 으로 매칭
  - 프로파일에 `expiration` 필드 직접 있으면 최우선 사용
- `config.profile_prefix` (regex) 로 화면에 노출할 프로파일 필터링 가능

### ECS
- **Cluster** 목록: STATUS · ACTIVE · RUNNING · PENDING
- **Service** 목록 (컬러): STATUS · EXEC(ON/OFF) · TASKS · LB(healthy) · SCALE(min~max)
- **우측 실시간 monitor pane 자동 표시** (터미널 폭 ≥70)
  - CPU / Memory (AWS/ECS) + Network Rx·Tx / Disk R·W (ContainerInsights)
  - 스파크라인 + now/avg/peak, `config.monitor_interval_sec` 주기 (기본 30s)
- 서비스 액션
  - `Enter` — 상태 상세 (Deployments · AutoScaling · CW metrics · TaskDef · LB targets · Events · Tasks)
  - `s` — ECS execute-command 셸 (`tea.ExecProcess`로 실제 TTY 위임 → vim/less 안정 동작)
  - `l` — 로그 뷰어 (첫 awslogs 컨테이너, 최근 1시간, `f`=tail follow, `/`=검색, 레벨 색상)
  - `t` — TaskDef JSON (색상 하이라이트, camelCase 변환)
  - `m` — 전용 모니터 화면 (자동 갱신)

### EC2 / SSM
- 인스턴스 목록 (terminated 제외 전부): ID · NAME · TYPE · **STATE**(색상) · IP · **PUB IP** · **SSM** · **USER**(OS 유추)
- USER 컬럼: AMI 이름/설명에서 유추 (`ec2-user`/`ubuntu`/`rocky`/`centos`/…)
- `Enter` — SSM start-session (접속 배너 자동 출력: `Connecting to: name (id)`, `Default user: ubuntu`)

### VPC (조회)
- 목록: VPC ID · NAME · CIDR · STATE · default flag
- `Enter` — 상세: **NAT Gateway (EIP + Private IP)** + Subnets (AZ · CIDR · public/private · 사용가능 IP)

### Secrets Manager (조회)
- 목록: NAME · LAST CHANGED · LAST ROTATED · KMS KEY
- `Enter` — 상세: 메타데이터 + Versions + Tags
- `v` — 값 조회 (⚠ 화면·터미널 히스토리 노출 경고와 함께 표시)

### Route 53 (조회)
- Hosted Zones 목록: ID · NAME · TYPE(public/private) · RECS · COMMENT
- `Enter` — Record 목록: NAME · TYPE(색상) · TTL · VALUE (Alias 는 `ALIAS → ...`)

### 공용 UX
- `/` 필터 (모든 목록 화면, 실시간)
- `숫자 키 (0-9)` — 목록 index 로 즉시 이동 (800ms 버퍼로 다자릿수 지원)
- `p` 프로파일 재선택 (스택 리셋)
- `g` 설정 화면 (수정 즉시 파일 저장)
- `c` 현재 뷰 클립보드 복사 (`pbcopy` → `wl-copy` → `xclip` 순, 없으면 `./awscx_output.txt`)
- `r` 새로고침 · `Esc/Backspace` 뒤로 · `q` 종료
- 상세/로그/taskdef 뷰는 리사이즈 대응 wrap (ANSI-aware)

## 요구사항

- Go 1.24 이상 (빌드 시)
- 런타임 시스템 도구
  - [aws-cli v2](https://docs.aws.amazon.com/cli/latest/userguide/getting-started-install.html) — ECS exec / SSM 실행에 사용
  - [session-manager-plugin](https://docs.aws.amazon.com/systems-manager/latest/userguide/session-manager-working-with-install-plugin.html)
- macOS / Linux (Windows 미지원)

## 빌드 · 설치

```bash
# 개발 (컴파일 없이 실행)
make run

# 현재 호스트용 정적 바이너리
make build          # → dist/awscx-go

# 크로스 컴파일 (배포용)
make release        # → dist/awscx-go-{linux,darwin}-{amd64,arm64}

# 의존성 정리 / 포맷 / 정적 분석 / 테스트
make tidy
make fmt vet test
```

빌드는 `CGO_ENABLED=0` 로 정적 링킹 되며, `glibc` 버전이나 Python 런타임에
의존하지 않고 그 자체로 실행 가능합니다.

## 사용

```bash
awscx-go                         # 프로파일 목록에서 선택 (TUI)
awscx-go -p my-profile           # 프로파일 지정
awscx-go -p my-profile -r us-east-1
awscx-go --no-tui                # bubbletea 대신 stdin 프롬프트 폴백

# 스크립트 용도 서브커맨드 (TUI 없이)
awscx-go -p my-profile clusters
awscx-go -p my-profile services <cluster>
awscx-go -p my-profile ec2
```

주요 키:
`Enter` 진입 · `s` 셸 · `l` 로그 · `f` tail · `t` taskdef · `m` monitor · `v` 값 조회(Secret) ·
`p` 프로파일 · `g` 설정 · `c` 복사 · `/` 필터 · `숫자` index 점프 · `Esc` 뒤로 · `q` 종료

## 설정 파일

`~/.config/awscx/config.json` 에 자동 생성됩니다. Python 판과 파일을 **공유**하므로
한쪽에서 편집한 값이 다른 쪽에도 반영됩니다.

`g` 로 앱 안에서 편집 가능. 항목:

- `region` — 기본 AWS 리전
- `command` — ECS exec 셸 (기본 `/bin/sh`)
- `log_enabled` / `log_level` / `log_path` — 앱 로그
- `monitor_interval_sec` — 모니터 갱신 주기 (기본 30초)
- `tail_interval_sec` — 로그 tail 폴링 주기 (기본 3초)
- `list_rows` · `terminal_color` · `mouse`
- **`profile_prefix`** — 프로파일 픽커에 노출할 이름 정규식 (예: `^sts-`, `(dev|prod)`)

## 디렉토리 구조

- `cmd/awscx-go/` — 엔트리포인트 (main + 서브커맨드 + stdin 폴백)
- `internal/aws/` — aws-sdk-go-v2 래퍼 (ECS · EC2 · SSM · CloudWatch · Logs · ELB · AAS · Secrets · Route53 · VPC · AMI OS user 유추 · 프로파일 토큰 파싱)
- `internal/config/` — `~/.config/awscx/config.json` 로드/저장 + `slog` 로거
- `internal/tui/` — bubbletea 화면 (프로파일 · 모드 · 클러스터 · 서비스 · 상세 · 로그 · taskdef · 모니터 · EC2 · VPC/subnet · Secrets/값 · Route53 zones/records · 설정)

## 권한 (IAM)

사용하는 기능만 필요합니다.

- ECS: `ecs:ListClusters` / `Describe*` / `ListServices` / `ListTasks` / `DescribeTasks` / `DescribeTaskDefinition` / `ExecuteCommand`
- EC2: `ec2:DescribeInstances` / `DescribeVpcs` / `DescribeSubnets` / `DescribeNatGateways` / `DescribeImages`
- SSM: `ssm:DescribeInstanceInformation` / `StartSession`
- CloudWatch: `cloudwatch:GetMetricStatistics`
- Logs: `logs:FilterLogEvents`
- ELBv2: `elasticloadbalancing:Describe*`
- App Auto Scaling: `application-autoscaling:Describe*`
- Secrets Manager: `secretsmanager:ListSecrets` / `DescribeSecret` (+ `GetSecretValue` 는 `v` 키 사용 시)
- Route 53: `route53:ListHostedZones` / `ListResourceRecordSets`

권한 없는 서비스는 해당 화면 진입 시 `AccessDenied` 배너로 표시됩니다 (다른 화면은 정상 동작).

## Python 판과의 차이

기능은 대부분 동등하지만 실装 방식이 다른 부분:

- **셸**: Python 은 pyte 기반 임베드 PTY 에뮬레이터를 우측 패널에 표시 → Go 는
  `tea.ExecProcess` 로 TUI 를 잠깐 내리고 aws CLI 에 실제 TTY 를 붙임 (vim/less 안정 동작)
- **모니터**: Python 은 사이드 패널 CSS split → Go 는 `lipgloss.JoinHorizontal` 로 스플릿 (터미널 폭 ≥70 자동)
- **taskdef JSON 하이라이팅**: Python 은 `rich.Syntax` → Go 는 자체 토크나이저 (chroma 등 외부 dep 없음)
- **테이블 색상**: bubbles/table 의 ANSI-unaware truncate 버그 회피 위해 컬러 셀이 있는 목록(서비스·EC2·프로파일 등) 은 lipgloss 직접 렌더링
- **추가된 것**: VPC / Secrets Manager / Route 53 조회 기능은 Go 판에만 있음

## License

MIT