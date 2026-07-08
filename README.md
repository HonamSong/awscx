# awscx

![version](https://img.shields.io/badge/version-0.1.0-blue.svg)
![python](https://img.shields.io/badge/python-3.11%2B-blue.svg)
![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)
![platform](https://img.shields.io/badge/platform-macOS%20%7C%20Linux-lightgrey.svg)

AWS **ECS exec** / **EC2 SSM** 접속을 위한 k9s 스타일 터미널 TUI.

프로파일을 고르고 → ECS(cluster/service) 또는 EC2(SSM) 를 골라 → 목록에서 바로
셸 접속, 로그(tail·검색·레벨색상), 실시간 모니터(CPU/Mem/Net/Disk), 상태(LB·오토스케일·taskdef)
를 한 화면에서 확인합니다.

## 주요 기능

- Profile 선택(번호 입력 지원) → ECS / EC2 선택
- ECS
  - cluster/service 목록: STATUS / EXEC(on·off) / TASKS / LB(healthy) / SCALE(min~max)
  - `Enter` 상태(서비스 상태·CloudWatch CPU/Mem·TaskDef cpu/mem/port·LB 헬스·오토스케일·이벤트, 색상)
  - `s` 컨테이너 shell 접속(임베드 터미널, 컬러 렌더)
  - `l` 로그 보기 → `/` 검색, `f` tail(follow), 로그레벨 색상
  - `t` task definition(JSON)
  - 서비스에 커서를 두면 아래에 실시간 모니터 자동 표시
- EC2
  - running 인스턴스 목록(SSM online 여부 표시) → `Enter` SSM 세션 접속
- 임베드 터미널: `F10` 으로 나가기, 붙여넣기 지원
- `g` 설정 화면(config 파일 편집), `c` 결과 클립보드 복사

## 요구사항 (pip 외 시스템 도구)

- Python >= 3.11
- [aws-cli v2](https://docs.aws.amazon.com/cli/latest/userguide/getting-started-install.html)
- [session-manager-plugin](https://docs.aws.amazon.com/systems-manager/latest/userguide/session-manager-working-with-install-plugin.html)
- macOS / Linux (Windows 미지원 — PTY/termios 사용)

## 설치

```bash
pip install awscx
```

## 사용

```bash
awscx                 # 프로파일 목록에서 선택
awscx -p my-profile   # 프로파일 지정
awscx -p my-profile -r us-east-1
# 또는
python -m awscx
```

주요 키: `Enter` 상태 · `s` 접속 · `l` 로그 · `f` tail · `t` taskdef · `m`(자동)
모니터 · `p` 프로파일 · `g` 설정 · `c` 복사 · `/` 필터 · `Ctrl+U/D` 결과 스크롤 ·
`F10` 세션 나가기 · `q` 종료

## 설정 파일

`~/.config/awscx/config.json` 에 자동 생성됩니다(region, command, 로그, 갱신주기 등).
앱 안에서 `g` 로 편집할 수 있습니다.

## 권한 (IAM)

기능에 따라 다음 권한이 필요합니다: `ecs:*`(조회/execute-command), `ec2:DescribeInstances`,
`ssm:DescribeInstanceInformation`, `ssm:StartSession`, `cloudwatch:GetMetricStatistics`,
`logs:FilterLogEvents`, `elasticloadbalancing:Describe*`,
`application-autoscaling:Describe*`.

## License

MIT
