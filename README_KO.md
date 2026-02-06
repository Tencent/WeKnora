<p align="center">
  <picture>
    <img src="./docs/images/logo.png" alt="WeKnora Logo" height="120"/>
  </picture>
</p>
<p align="center">
  <picture>
    <a href="https://trendshift.io/repositories/15289" target="_blank">
      <img src="https://trendshift.io/api/badge/repositories/15289" alt="Tencent%2FWeKnora | Trendshift" style="width: 250px; height: 55px;" width="250" height="55"/>
    </a>
  </picture>
</p>

<p align="center">
    <a href="https://weknora.weixin.qq.com" target="_blank">
        <img alt="공식 사이트" src="https://img.shields.io/badge/공식_사이트-WeKnora-4e6b99">
    </a>
    <a href="https://chatbot.weixin.qq.com" target="_blank">
        <img alt="WeChat 대화 오픈 플랫폼" src="https://img.shields.io/badge/WeChat_대화_오픈_플랫폼-5ac725">
    </a>
    <a href="https://github.com/Tencent/WeKnora/blob/main/LICENSE">
        <img src="https://img.shields.io/badge/License-MIT-ffffff?labelColor=d4eaf7&color=2e6cc4" alt="License">
    </a>
    <a href="./CHANGELOG.md">
        <img alt="버전" src="https://img.shields.io/badge/version-0.2.10-2e6cc4?labelColor=d4eaf7">
    </a>
</p>

<p align="center">
| <a href="./README.md"><b>English</b></a> | <a href="./README_CN.md"><b>简体中文</b></a> | <a href="./README_JA.md"><b>日本語</b></a> | <b>한국어</b> |
</p>

<p align="center">
  <h4 align="center">

  [프로젝트 소개](#-프로젝트-소개) • [아키텍처 설계](#️-아키텍처-설계) • [핵심 기능](#-핵심-기능) • [빠른 시작](#-빠른-시작) • [문서](#-문서) • [개발 가이드](#-개발-가이드)

  </h4>
</p>

# 💡 WeKnora - 대규모 언어 모델 기반 문서 이해 검색 프레임워크

## 📌 프로젝트 소개

[**WeKnora(위노라)**](https://weknora.weixin.qq.com)는 대규모 언어 모델(LLM)을 기반으로 한 문서 이해 및 시맨틱 검색 프레임워크로, 구조가 복잡하고 내용이 이질적인 문서 시나리오를 위해 특별히 설계되었습니다.

프레임워크는 모듈러 아키텍처를 채택하여 멀티모달 전처리, 시맨틱 벡터 인덱싱, 지능형 검색, 대규모 모델 생성 추론을 통합하여 효율적이고 제어 가능한 문서 Q&A 워크플로우를 구축합니다. 핵심 검색 프로세스는 **RAG(Retrieval-Augmented Generation)** 메커니즘을 기반으로 하며, 문맥 관련 프래그먼트와 언어 모델을 결합하여 더 높은 품질의 시맨틱 응답을 실현합니다.

**공식 사이트:** https://weknora.weixin.qq.com

## ✨ 최신 업데이트

**v0.2.0 버전 하이라이트:**

- 🤖 **Agent 모드**: 새로운 ReACT Agent 모드 추가, 내장 도구, MCP 도구, 웹 검색을 호출하고, 여러 번의 반복과 리플렉션을 통해 포괄적인 요약 리포트 제공
- 📚 **다중 타입 지식 베이스**: FAQ와 문서 두 가지 유형의 지식 베이스 지원, 폴더 가져오기, URL 가져오기, 태그 관리, 온라인 입력 기능 신규 추가
- ⚙️ **대화 전략**: Agent 모델, 일반 모드 모델, 검색 임계값, Prompt 설정 지원, 멀티턴 대화 동작을 정밀하게 제어
- 🌐 **웹 검색**: 확장 가능한 웹 검색 엔진 지원, DuckDuckGo 검색 엔진 내장
- 🔌 **MCP 도구 통합**: MCP를 통해 Agent 기능 확장, uvx, npx 실행 도구 내장, 다중 전송 방식 지원
- 🎨 **새로운 UI**: 대화 인터페이스 최적화, Agent 모드/일반 모드 전환, 도구 호출 프로세스 표시, 지식 베이스 관리 인터페이스 전면 업그레이드
- ⚡ **인프라 업그레이드**: MQ 비동기 작업 관리 도입, 데이터베이스 자동 마이그레이션 지원, 빠른 개발 모드 제공

## 🔒 보안 공지

**중요:** v0.1.3 버전부터 WeKnora에는 시스템 보안을 강화하기 위한 로그인 인증 기능이 포함되어 있습니다. v0.2.0에서는 더 많은 기능 강화와 개선이 추가되었습니다. 프로덕션 환경 배포 시 다음을 강력히 권장합니다:

- WeKnora 서비스는 공용 인터넷이 아닌 내부/프라이빗 네트워크 환경에 배포하세요
- 중요한 정보 유출을 방지하기 위해 서비스를 직접 공용 네트워크에 노출하지 마세요
- 배포 환경에 적절한 방화벽 규칙과 접근 제어를 설정하세요
- 보안 패치 및 개선을 위해 정기적으로 최신 버전으로 업데이트하세요

## 🏗️ 아키텍처 설계

![weknora-pipelone.png](./docs/images/architecture.png)

WeKnora는 현대적인 모듈러 설계를 채택하여 완전한 문서 이해 및 검색 파이프라인을 구축합니다. 시스템에는 주로 문서 파싱, 벡터화 처리, 검색 엔진, 대규모 모델 추론 등의 핵심 모듈이 포함되어 있으며, 각 컴포넌트는 유연하게 설정 및 확장할 수 있습니다.

## 🎯 핵심 기능

- **🤖 Agent 모드**: ReACT Agent 모드 지원, 내장 도구로 지식 베이스 검색, MCP 도구 및 웹 검색 호출, 여러 번의 반복과 리플렉션을 통해 포괄적인 요약 리포트 제공
- **🔍 정확한 이해**: PDF, Word, 이미지 등 문서의 구조화된 콘텐츠 추출을 지원하고, 통합된 시맨틱 뷰 구축
- **🧠 지능형 추론**: 대규모 언어 모델을 활용하여 문서 컨텍스트와 사용자 의도를 이해하고, 정확한 Q&A 및 멀티턴 대화 지원
- **📚 다중 타입 지식 베이스**: FAQ와 문서 두 가지 유형의 지식 베이스 지원, 폴더 가져오기, URL 가져오기, 태그 관리, 온라인 입력 기능
- **🔧 유연한 확장**: 파싱, 임베딩, 검색에서 생성까지 전체 프로세스를 분리하여 유연한 통합 및 커스텀 확장 용이
- **⚡ 효율적인 검색**: 다중 검색 전략 하이브리드: 키워드, 벡터, 지식 그래프, 크로스 지식 베이스 검색 지원
- **🌐 웹 검색**: 확장 가능한 웹 검색 엔진 지원, DuckDuckGo 검색 엔진 내장
- **🔌 MCP 도구 통합**: MCP를 통해 Agent 기능 확장, uvx, npx 실행 도구 내장, 다중 전송 방식 지원
- **⚙️ 대화 전략**: Agent 모델, 일반 모드 모델, 검색 임계값, Prompt 설정 지원, 멀티턴 대화 동작을 정밀하게 제어
- **🎯 사용 편의성**: 직관적인 웹 인터페이스와 표준 API, 기술적 장벽 없이 빠르게 시작 가능
- **🔒 보안 및 제어**: 로컬 및 프라이빗 클라우드 배포 지원, 데이터는 완전히 자체 관리 가능

## 📊 적용 시나리오

| 응용 시나리오 | 구체적인 응용 | 핵심 가치 |
|---------|----------|----------|
| **기업 지식 관리** | 내부 문서 검색, 규정 Q&A, 운영 매뉴얼 조회 | 지식 검색 효율 향상, 교육 비용 절감 |
| **과학 연구 문헌 분석** | 논문 검색, 연구 리포트 분석, 학술 자료 정리 | 문헌 조사 가속화, 연구 의사결정 지원 |
| **제품 기술 지원** | 제품 매뉴얼 Q&A, 기술 문서 검색, 문제 해결 | 고객 서비스 품질 향상, 기술 지원 부담 경감 |
| **법적 컴플라이언스 검토** | 계약 조항 검색, 법규 정책 조회, 케이스 분석 | 컴플라이언스 효율 향상, 법적 리스크 감소 |
| **의료 지식 지원** | 의학 문헌 검색, 진료 가이드라인 조회, 증례 분석 | 임상 의사결정 지원, 진료 품질 향상 |

## 🧩 기능 모듈 능력

| 기능 모듈 | 지원 현황 | 설명 |
|---------|-----------------------------------------------------|------|
| Agent 모드 | ✅ ReACT Agent 모드 | 내장 도구로 지식 베이스 검색, MCP 도구 및 웹 검색 사용, 크로스 지식 베이스 검색, 여러 번의 반복 및 리플렉션 지원 |
| 지식 베이스 타입 | ✅ FAQ / 문서 | FAQ와 문서 두 가지 유형의 지식 베이스 생성 지원, 폴더 가져오기, URL 가져오기, 태그 관리, 온라인 입력 기능 |
| 문서 포맷 지원 | ✅ PDF / Word / Txt / Markdown / 이미지(OCR / Caption 포함) | 다양한 구조화/비구조화 문서 콘텐츠 파싱 지원, 그림-텍스트 혼합 및 이미지 텍스트 추출 지원 |
| 모델 관리 | ✅ 중앙 집중 설정, 내장 모델 공유 | 모델 중앙 집중 설정, 지식 베이스 설정 페이지에 모델 선택 추가, 멀티테넌트 간 내장 모델 공유 지원 |
| 임베딩 모델 지원 | ✅ 로컬 모델, BGE / GTE API 등 | 커스텀 embedding 모델 지원, 로컬 배포 및 클라우드 벡터 생성 인터페이스 호환 |
| 벡터 데이터베이스 연결 | ✅ PostgreSQL(pgvector), Elasticsearch | 주류 벡터 인덱스 백엔드 지원, 유연한 전환 및 확장 가능, 다양한 검색 시나리오에 적응 |
| 검색 메커니즘 | ✅ BM25 / Dense Retrieve / GraphRAG | 밀집/희소 검색, 지식 그래프 강화 검색 등 다중 전략 지원, 검색-재순위-생성 프로세스 자유롭게 조합 가능 |
| 대규모 모델 통합 | ✅ Qwen, DeepSeek 등 지원, 사고/비사고 모드 전환 | 로컬 대규모 모델(Ollama 실행 등)에 연결 가능, 또는 외부 API 서비스 호출, 추론 모드 유연한 설정 지원 |
| 대화 전략 | ✅ Agent 모델, 일반 모드 모델, 검색 임계값, Prompt 설정 | Agent 모델, 일반 모드에 필요한 모델, 검색 임계값 설정 지원, 온라인 Prompt 설정, 멀티턴 대화 동작 정밀 제어 |
| 웹 검색 | ✅ 확장 가능한 검색 엔진, DuckDuckGo / Google | 확장 가능한 웹 검색 엔진 지원, DuckDuckGo 검색 엔진 내장 |
| MCP 도구 | ✅ uvx, npx 실행 도구, Stdio/HTTP Streamable/SSE | MCP를 통해 Agent 기능 확장, uvx, npx 두 가지 MCP 실행 도구 내장, 세 가지 전송 방식 지원 |
| Q&A 능력 | ✅ 컨텍스트 인식, 멀티턴 대화, 프롬프트 템플릿 | 복잡한 시맨틱 모델링, 지시 제어, 체인 Q&A 지원, 프롬프트 및 컨텍스트 윈도우 설정 가능 |
| 엔드투엔드 테스트 지원 | ✅ 검색+생성 프로세스 시각화 및 지표 평가 | 통합 링크 테스트 도구 제공, 리콜 적중률, 응답 커버리지, BLEU/ROUGE 등 주류 지표 평가 지원 |
| 배포 모드 | ✅ 로컬 배포 / Docker 이미지 | 프라이빗화, 오프라인 배포, 유연한 운영 유지보수 요구사항 충족, 빠른 개발 모드 지원 |
| 사용자 인터페이스 | ✅ Web UI + RESTful API | 인터랙티브 인터페이스 및 표준 API 인터페이스 제공, Agent 모드/일반 모드 전환, 도구 호출 프로세스 표시 지원 |
| 작업 관리 | ✅ MQ 비동기 작업, 데이터베이스 자동 마이그레이션 | MQ를 통한 비동기 작업 상태 유지 도입, 버전 업그레이드 시 데이터베이스 테이블 구조 및 데이터 자동 마이그레이션 지원 |

## 🚀 빠른 시작

### 🛠 환경 요구사항

다음 도구가 로컬에 설치되어 있는지 확인하세요:

* [Docker](https://www.docker.com/)
* [Docker Compose](https://docs.docker.com/compose/)
* [Git](https://git-scm.com/)

### 📦 설치 단계

#### ① 코드 저장소 클론

```bash
# 메인 저장소 클론
git clone https://github.com/Tencent/WeKnora.git
cd WeKnora
```

#### ② 환경 변수 설정

```bash
# 샘플 설정 파일 복사
cp .env.example .env

# .env를 편집하고 해당 설정 정보 입력
# 모든 변수의 설명은 .env.example의 주석 참조
```

#### ③ 서비스 시작 (Ollama 포함)

.env 파일에서 시작해야 할 이미지를 확인합니다.

```bash
./scripts/start_all.sh
```

또는

```bash
make start-all
```

#### ③.0 ollama 서비스 시작 (선택사항)

```bash
ollama serve > /dev/null 2>&1 &
```

#### ③.1 다양한 기능 조합 활성화

- 최소 핵심 서비스
```bash
docker compose up -d
```

- 모든 기능 활성화
```bash
docker-compose --profile full up -d
```

- 트레이스 로그 필요
```bash
docker-compose --profile jaeger up -d
```

- Neo4j 지식 그래프 필요
```bash
docker-compose --profile neo4j up -d
```

- Minio 파일 스토리지 서비스 필요
```bash
docker-compose --profile minio up -d
```

- 여러 옵션 조합
```bash
docker-compose --profile neo4j --profile minio up -d
```

#### ④ 서비스 중지

```bash
./scripts/start_all.sh --stop
# 또는
make stop-all
```

### 🌐 서비스 접속 주소

시작 성공 후, 다음 주소로 접속할 수 있습니다:

* Web UI: `http://localhost`
* 백엔드 API: `http://localhost:8080`
* 링크 트레이스 (Jaeger): `http://localhost:16686`

### 🔌 WeChat 대화 오픈 플랫폼 사용

WeKnora는 [WeChat 대화 오픈 플랫폼](https://chatbot.weixin.qq.com)의 핵심 기술 프레임워크로서, 더 쉬운 사용 방법을 제공합니다:

- **노코드 배포**: 지식을 업로드하기만 하면 WeChat 생태계에서 빠르게 지능형 Q&A 서비스를 배포하고, "즉시 질문하고 즉시 답변" 경험 실현
- **효율적인 문제 관리**: 고빈도 문제의 독립적인 분류 관리 지원, 풍부한 데이터 도구 제공으로 정확하고 신뢰성 높으며 유지보수가 쉬운 응답 보장
- **WeChat 생태계 커버리지**: WeChat 대화 오픈 플랫폼을 통해 WeKnora의 지능형 Q&A 능력을 공식 계정, 미니 프로그램 등 WeChat 시나리오에 원활하게 통합하여 사용자 상호작용 경험 향상

### 🔗 MCP 서버를 사용하여 배포된 WeKnora에 접속

#### 1️⃣ 저장소 클론
```
git clone https://github.com/Tencent/WeKnora
```

#### 2️⃣ MCP 서버 설정

> 설정은 직접 [MCP 설정 설명](./mcp-server/MCP_CONFIG.md)을 참조하는 것을 권장합니다.

MCP 클라이언트에서 서버 설정
```json
{
  "mcpServers": {
    "weknora": {
      "args": [
        "path/to/WeKnora/mcp-server/run_server.py"
      ],
      "command": "python",
      "env":{
        "WEKNORA_API_KEY":"WeKnora 인스턴스에 접속하여 개발자 도구를 열고 요청 헤더 x-api-key 확인, sk로 시작",
        "WEKNORA_BASE_URL":"http(s)://당신의-WeKnora-주소/api/v1"
      }
    }
  }
}
```

stdio 명령으로 직접 실행
```
pip install weknora-mcp-server
python -m weknora-mcp-server
```

## 🔧 초기 설정 가이드

사용자가 다양한 모델을 빠르게 설정하고 시행착오 비용을 줄일 수 있도록, 기존의 설정 파일 초기화 방식을 개선하여 Web UI 인터페이스를 추가하여 다양한 모델 설정을 할 수 있게 했습니다. 사용 전에 코드가 최신 버전으로 업데이트되어 있는지 확인하세요. 구체적인 사용 단계는 다음과 같습니다:
본 프로젝트를 처음 사용하는 경우, ①② 단계를 건너뛰고 직접 ③④ 단계로 진행하세요.

### ① 서비스 중지

```bash
./scripts/start_all.sh --stop
```

### ② 기존 데이터 테이블 초기화 (중요한 데이터가 없는 경우 권장)

```bash
make clean-db
```

### ③ 컴파일 및 서비스 시작

```bash
./scripts/start_all.sh
```

### ④ Web UI 접속

http://localhost

처음 접속 시 자동으로 등록/로그인 페이지로 이동합니다. 등록 완료 후, 새로운 지식 베이스를 생성하고 해당 설정 화면에서 필요한 항목을 구성하세요.

## 📱 기능 데모

### Web UI 인터페이스

<table>
  <tr>
    <td><b>지식 베이스 관리</b><br/><img src="./docs/images/knowledgebases.png" alt="지식 베이스 관리"></td>
    <td><b>대화 설정</b><br/><img src="./docs/images/settings.png" alt="대화 설정"></td>
  </tr>
  <tr>
    <td colspan="2"><b>Agent 모드 도구 호출 프로세스</b><br/><img src="./docs/images/agent-qa.png" alt="Agent 모드 도구 호출 프로세스"></td>
  </tr>
</table>

**지식 베이스 관리:** FAQ와 문서 두 가지 유형의 지식 베이스 생성 지원, 드래그 앤 드롭 업로드, 폴더 가져오기, URL 가져오기 등 다양한 방법 지원, 문서 구조를 자동 인식하여 핵심 지식을 추출하고 인덱스를 구축합니다. 태그 관리 및 온라인 입력 지원, 시스템은 처리 진행 상황과 문서 상태를 명확하게 표시하여 효율적인 지식 베이스 관리를 실현합니다.

**Agent 모드:** ReACT Agent 모드 활성화 지원, 내장 도구로 지식 베이스 검색, 사용자가 설정한 MCP 도구 및 웹 검색 도구를 호출하여 외부 서비스에 접근, 여러 번의 반복과 리플렉션을 통해 최종적으로 포괄적인 요약 리포트를 제공합니다. 크로스 지식 베이스 검색 지원, 여러 지식 베이스를 동시에 검색할 수 있습니다.

**대화 전략:** Agent 모델, 일반 모드에 필요한 모델, 검색 임계값 설정 지원, 온라인 Prompt 설정 지원, 멀티턴 대화 동작 및 검색 리콜 실행 방법을 정밀하게 제어합니다. 대화 입력 상자는 Agent 모드/일반 모드 전환 지원, 웹 검색 활성화/비활성화 지원, 대화 모델 선택 지원합니다.

### 문서 지식 그래프

WeKnora는 문서를 지식 그래프로 변환하여 문서 내 서로 다른 단락 간의 관련 관계를 표시하는 것을 지원합니다. 지식 그래프 기능을 활성화하면 시스템이 문서 내부의 시맨틱 관련 네트워크를 분석하고 구축하여, 사용자가 문서 내용을 이해하는 데 도움을 줄 뿐만 아니라 인덱싱 및 검색에 구조화된 지원을 제공하여 검색 결과의 관련성과 폭을 향상시킵니다.

자세한 설정은 [지식 그래프 설정 가이드](./docs/KnowledgeGraph.md)를 참조하세요.

### 지원 MCP 서버

[MCP 설정 가이드](./mcp-server/MCP_CONFIG.md)를 참조하여 필요한 설정을 진행하세요.


## 📘 문서

자주 묻는 질문 해결: [자주 묻는 질문](./docs/QA.md)

자세한 API 문서는: [API 문서](./docs/api/README.md)를 참조하세요

## 🧭 개발 가이드

### ⚡ 빠른 개발 모드 (권장)

코드를 자주 수정해야 하는 경우, **매번 Docker 이미지를 다시 빌드할 필요가 없습니다**! 빠른 개발 모드를 사용하세요:

```bash
# 방법 1: Make 명령 사용 (권장)
make dev-start      # 인프라 시작
make dev-app        # 백엔드 시작 (새 터미널)
make dev-frontend   # 프론트엔드 시작 (새 터미널)

# 방법 2: 원클릭 시작
./scripts/quick-dev.sh

# 방법 3: 스크립트 사용
./scripts/dev.sh start     # 인프라 시작
./scripts/dev.sh app       # 백엔드 시작 (새 터미널)
./scripts/dev.sh frontend  # 프론트엔드 시작 (새 터미널)
```

**개발의 장점:**
- ✅ 프론트엔드 변경 시 자동 핫 리로드 (재시작 불필요)
- ✅ 백엔드 변경 시 빠른 재시작 (5-10초, Air 핫 리로드 지원)
- ✅ Docker 이미지 재빌드 불필요
- ✅ IDE 브레이크포인트 디버깅 지원

**상세 문서:** [개발 환경 빠른 시작](./docs/DevelopmentGuide.md)

### 📁 프로젝트 디렉토리 구조

```
WeKnora/  
├── client/      # Go 클라이언트  
├── cmd/         # 애플리케이션 엔트리  
├── config/      # 설정 파일  
├── docker/      # Docker 이미지 파일  
├── docreader/   # 문서 파싱 프로젝트  
├── docs/        # 프로젝트 문서  
├── frontend/    # 프론트엔드 프로젝트  
├── internal/    # 핵심 비즈니스 로직  
├── mcp-server/  # MCP 서버  
├── migrations/  # 데이터베이스 마이그레이션 스크립트  
└── scripts/     # 시작 및 도구 스크립트
```

## 🤝 기여 가이드

커뮤니티 사용자의 기여를 환영합니다! 제안, 버그, 새로운 기능 요청이 있는 경우 [Issue](https://github.com/Tencent/WeKnora/issues)를 통해 제출하거나 직접 Pull Request를 제출해 주세요.

### 🎯 기여 방법

- 🐛 **버그 수정**: 시스템 결함을 발견하고 수정
- ✨ **새로운 기능**: 새로운 기능을 제안하고 구현
- 📚 **문서 개선**: 프로젝트 문서 개선
- 🧪 **테스트 케이스**: 유닛 테스트 및 통합 테스트 작성
- 🎨 **UI/UX 최적화**: 사용자 인터페이스 및 경험 개선

### 📋 기여 흐름

1. **프로젝트 Fork** 하여 본인의 GitHub 계정으로
2. **기능 브랜치 생성** `git checkout -b feature/amazing-feature`
3. **변경 사항 커밋** `git commit -m 'Add amazing feature'`
4. **브랜치 푸시** `git push origin feature/amazing-feature`
5. **Pull Request 생성** 후 변경 내용 상세히 설명

### 🎨 코드 규약

- [Go Code Review Comments](https://github.com/golang/go/wiki/CodeReviewComments) 준수
- `gofmt`을 사용하여 코드 포맷팅
- 필요한 유닛 테스트 추가
- 관련 문서 업데이트

### 📝 커밋 규약

[Conventional Commits](https://www.conventionalcommits.org/) 규약 사용:

```
feat: 문서 일괄 업로드 기능 추가
fix: 벡터 검색 정확도 문제 수정
docs: API 문서 업데이트
test: 검색 엔진 테스트 케이스 추가
refactor: 문서 파싱 모듈 리팩토링
```

## 👥 컨트리뷰터

훌륭한 컨트리뷰터들께 감사드립니다:

[![Contributors](https://contrib.rocks/image?repo=Tencent/WeKnora )](https://github.com/Tencent/WeKnora/graphs/contributors )

## 📄 라이선스

이 프로젝트는 [MIT](./LICENSE) 라이선스 하에 공개되어 있습니다.
이 프로젝트의 코드를 자유롭게 사용, 수정, 배포할 수 있지만, 원본 저작권 표시를 유지해야 합니다.

## 📈 프로젝트 통계

<a href="https://www.star-history.com/#Tencent/WeKnora&type=date&legend=top-left">
 <picture>
   <source media="(prefers-color-scheme: dark)" srcset="https://api.star-history.com/svg?repos=Tencent/WeKnora&type=date&theme=dark&legend=top-left" />
   <source media="(prefers-color-scheme: light)" srcset="https://api.star-history.com/svg?repos=Tencent/WeKnora&type=date&legend=top-left" />
   <img alt="Star History Chart" src="https://api.star-history.com/svg?repos=Tencent/WeKnora&type=date&legend=top-left" />
 </picture>
</a>
