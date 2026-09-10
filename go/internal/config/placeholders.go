package config

// Placeholder strings shown in the UI when a pane has no selection yet.
// Kept together here (matching python/awscx/config.py) so both implementations
// stay textually in sync.

const (
	OutputPlaceholder = "상단에서 항목을 선택하세요.\n\n" +
		"  Enter  상태\n  s  shell 접속\n  l  로그\n  t  task definition\n\n" +
		"서비스에 커서를 두면 우측에 실시간 모니터가\n자동으로 표시됩니다."

	ProfilePlaceholder = "먼저 상단에서 프로파일을 선택하세요.\n\n" +
		"프로파일을 고르면(Enter) 접근 대상(ECS/EC2)을\n" +
		"선택하는 화면이 나옵니다.\n\n" +
		"  /  프로파일 필터\n  q  종료"

	ModePlaceholder = "접근 대상을 선택하세요.\n\n" +
		"  1) ECS  (cluster/service exec)\n" +
		"  2) EC2  (SSM session)"

	EC2Placeholder = "상단에서 EC2 인스턴스를 선택하세요.\n\n" +
		"  Enter  SSM 세션 접속\n" +
		"  번호   커서 이동\n\n" +
		"SSM=online 인 인스턴스만 접속됩니다.\n" +
		"(로그·task definition·모니터는 ECS 전용)"
)