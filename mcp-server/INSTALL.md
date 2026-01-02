# WeKnora MCP Server 설치 및 사용 가이드

## 빠른 시작

### 1. 의존성 설치
```bash
pip install -r requirements.txt
```

### 2. 환경 변수 설정
```bash
# Linux/macOS
export WEKNORA_BASE_URL="http://localhost:8080/api/v1"
export WEKNORA_API_KEY="your_api_key_here"

# Windows PowerShell
$env:WEKNORA_BASE_URL="http://localhost:8080/api/v1"
$env:WEKNORA_API_KEY="your_api_key_here"

# Windows CMD
set WEKNORA_BASE_URL=http://localhost:8080/api/v1
set WEKNORA_API_KEY=your_api_key_here
```

### 3. 서버 실행

서버를 실행하는 방법에는 여러 가지가 있습니다:

#### 방법 1: 메인 진입점 사용 (권장)
```bash
python main.py
```

#### 방법 2: 원본 시작 스크립트 사용
```bash
python run_server.py
```

#### 방법 3: 서버 모듈 직접 실행
```bash
python weknora_mcp_server.py
```

#### 방법 4: Python 모듈로 실행
```bash
python -m weknora_mcp_server
```

## Python 패키지로 설치

### 개발 모드 설치
```bash
pip install -e .
```

설치 후 명령줄 도구를 사용할 수 있습니다:
```bash
weknora-mcp-server
# 또는
weknora-server
```

### 프로덕션 모드 설치
```bash
pip install .
```

### 배포 패키지 빌드
```bash
# 소스 배포 패키지 및 휠 빌드
python setup.py sdist bdist_wheel

# 또는 build 도구 사용
pip install build
python -m build
```

## 명령줄 옵션

메인 진입점 `main.py`는 다음 옵션을 지원합니다:

```bash
python main.py --help                 # 도움말 표시
python main.py --check-only           # 환경 구성만 확인
python main.py --verbose              # 상세 로그 활성화
python main.py --version              # 버전 정보 표시
```

## 환경 확인

다음 명령을 실행하여 환경 구성을 확인합니다:
```bash
python main.py --check-only
```

다음과 같은 정보가 표시됩니다:
- WeKnora API 기본 URL 구성
- API 키 설정 상태
- 의존성 패키지 설치 상태

## 문제 해결

### 1. 가져오기 오류
`ImportError`가 발생하면 다음을 확인하십시오:
- 모든 의존성 설치: `pip install -r requirements.txt`
- Python 버전 호환성 (3.10+ 권장)
- 파일 이름 충돌 없음

### 2. 연결 오류
WeKnora API에 연결할 수 없는 경우:
- `WEKNORA_BASE_URL`이 올바른지 확인
- WeKnora 서비스가 실행 중인지 확인
- 네트워크 연결 확인

### 3. 인증 오류
인증 문제가 발생한 경우:
- `WEKNORA_API_KEY` 설정 확인
- API 키가 유효한지 확인
- 권한 설정 확인

## 개발 모드

### 프로젝트 구조
```
WeKnoraMCP/
├── __init__.py              # 패키지 초기화 파일
├── main.py                  # 메인 진입점
├── run_server.py           # 원본 시작 스크립트
├── weknora_mcp_server.py   # MCP 서버 구현
├── requirements.txt        # 의존성 목록
├── setup.py               # 설치 스크립트
├── MANIFEST.in            # 포함 파일 목록
├── LICENSE                # 라이선스
├── README.md              # 프로젝트 설명
└── INSTALL.md             # 설치 가이드
```

### 새 기능 추가
1. `WeKnoraClient` 클래스에 새 API 메서드 추가
2. `handle_list_tools()`에 새 도구 등록
3. `handle_call_tool()`에 도구 로직 구현
4. 문서 및 테스트 업데이트

### 테스트
```bash
# 기본 테스트 실행
python test_imports.py

# 환경 구성 테스트
python main.py --check-only

# 서버 시작 테스트
python main.py --verbose
```

## 배포

### Docker 배포
`Dockerfile` 생성:
```dockerfile
FROM python:3.11-slim

WORKDIR /app
COPY requirements.txt .
RUN pip install -r requirements.txt

COPY . .
RUN pip install -e .

ENV WEKNORA_BASE_URL=http://localhost:8080/api/v1
EXPOSE 8000

CMD ["weknora-mcp-server"]
```

### 시스템 서비스
systemd 서비스 파일 `/etc/systemd/system/weknora-mcp.service` 생성:
```ini
[Unit]
Description=WeKnora MCP Server
After=network.target

[Service]
Type=simple
User=weknora
WorkingDirectory=/opt/weknora-mcp
Environment=WEKNORA_BASE_URL=http://localhost:8080/api/v1
Environment=WEKNORA_API_KEY=your_api_key
ExecStart=/usr/local/bin/weknora-mcp-server
Restart=always

[Install]
WantedBy=multi-user.target
```

서비스 활성화:
```bash
sudo systemctl enable weknora-mcp
sudo systemctl start weknora-mcp
```

## 지원

문제가 발생하면 다음을 수행하십시오:
1. 로그 출력 확인
2. 환경 구성 확인
3. 문제 해결 섹션 참조
4. 프로젝트 저장소에 Issue 제출: https://github.com/NannaOlympicBroadcast/WeKnoraMCP/issues
