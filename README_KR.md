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
        <img alt="공식 웹사이트" src="https://img.shields.io/badge/공식_웹사이트-WeKnora-4e6b99">
    </a>
    <a href="https://chatbot.weixin.qq.com" target="_blank">
        <img alt="WeChat 대화형 오픈 플랫폼" src="https://img.shields.io/badge/WeChat_대화형_오픈_플랫폼-5ac725">
    </a>
    <a href="https://github.com/Tencent/WeKnora/blob/main/LICENSE">
        <img src="https://img.shields.io/badge/License-MIT-ffffff?labelColor=d4eaf7&color=2e6cc4" alt="License">
    </a>
    <a href="./CHANGELOG.md">
        <img alt="버전" src="https://img.shields.io/badge/version-0.2.6-2e6cc4?labelColor=d4eaf7">
    </a>
</p>

<p align="center">
| <a href="./README.md"><b>English</b></a> | <b>한국어</b> | <a href="./README_JA.md"><b>日本語</b></a> |
</p>

<p align="center">
  <h4 align="center">

  [프로젝트 소개](#-프로젝트-소개) • [아키텍처 설계](#-아키텍처-설계) • [핵심 기능](#-핵심-기능) • [시작하기](#-시작하기) • [문서](#-문서) • [개발 가이드](#-개발-가이드)

  </h4>
</p>

# 💡 WeKnora - 대규모 언어 모델 기반 문서 이해 및 검색 프레임워크

## 📌 프로젝트 소개

[**WeKnora**](https://weknora.weixin.qq.com)는 복잡한 구조와 이질적인 콘텐츠를 가진 문서 시나리오를 위해 설계된 대규모 언어 모델(LLM) 기반의 문서 이해 및 의미론적 검색 프레임워크입니다.

이 프레임워크는 모듈식 아키텍처를 채택하여 멀티모달 전처리, 의미론적 벡터 인덱싱, 지능형 리콜 및 대규모 모델 생성 추론을 융합하여 효율적이고 제어 가능한 문서 질의응답 프로세스를 구축합니다. 핵심 검색 프로세스는 **RAG (Retrieval-Augmented Generation)** 메커니즘을 기반으로 하여 문맥 관련 조각을 언어 모델과 결합하여 더 높은 품질의 의미론적 답변을 구현합니다.

**공식 웹사이트:** https://weknora.weixin.qq.com

## ✨ 최신 업데이트

**v0.2.0 주요 특징:**

- 🤖 **Agent 모드**: 내장 도구, MCP 도구 및 웹 검색을 호출할 수 있는 ReACT Agent 모드를 추가하여 여러 번의 반복과 반성을 통해 포괄적인 요약 보고서를 제공합니다.
- 📚 **다중 유형 지식 베이스**: FAQ 및 문서 두 가지 유형의 지식 베이스를 지원하며, 폴더 가져오기, URL 가져오기, 태그 관리 및 온라인 입력 기능이 추가되었습니다.
- ⚙️ **대화 전략**: Agent 모델, 일반 모드 모델, 검색 임계값 및 프롬프트를 구성할 수 있어 다중 턴 대화 동작을 정밀하게 제어할 수 있습니다.
- 🌐 **웹 검색**: 확장 가능한 웹 검색 엔진을 지원하며, DuckDuckGo 검색 엔진이 내장되어 있습니다.
- 🔌 **MCP 도구 통합**: MCP를 통해 Agent 기능을 확장할 수 있으며, uvx 및 npx 실행 도구가 내장되어 있고 다양한 전송 방식을 지원합니다.
- 🎨 **새로운 UI**: 대화 인터페이스를 최적화하여 Agent 모드/일반 모드 전환을 지원하고, 도구 호출 과정을 표시하며, 지식 베이스 관리 인터페이스를 전면 업그레이드했습니다.
- ⚡ **인프라 업그레이드**: MQ 비동기 작업 관리를 도입하고, 데이터베이스 자동 마이그레이션을 지원하며, 빠른 개발 모드를 제공합니다.

## 🔒 보안 알림

**중요:** v0.1.3 버전부터 WeKnora는 시스템 보안을 강화하기 위해 로그인 인증 기능을 제공합니다. 운영 환경 배포 시 다음을 강력히 권장합니다.

- WeKnora 서비스를 공용 네트워크 환경이 아닌 내부 네트워크/사설 네트워크 환경에 배포하십시오.
- 중요 정보 유출 위험을 방지하기 위해 서비스를 공용 네트워크에 직접 노출하지 마십시오.
- 배포 환경에 적절한 방화벽 규칙과 액세스 제어를 구성하십시오.
- 보안 패치 및 개선 사항을 얻기 위해 정기적으로 최신 버전으로 업데이트하십시오.

## 🏗️ 아키텍처 설계

![weknora-architecture.png](./docs/images/architecture.png)

WeKnora는 현대적인 모듈식 설계를 채택하여 완전한 문서 이해 및 검색 파이프라인을 구축했습니다. 시스템은 주로 문서 파싱, 벡터 처리, 검색 엔진 및 대규모 모델 추론과 같은 핵심 모듈을 포함하며, 각 구성 요소는 유연하게 구성하고 확장할 수 있습니다.

## 🎯 핵심 기능

- **🤖 Agent 모드**: ReACT Agent 모드를 지원하여 내장 도구로 지식 베이스를 검색하고, MCP 도구 및 웹 검색을 호출하여 외부 서비스에 액세스할 수 있으며, 여러 번의 반복과 반성을 통해 포괄적인 요약 보고서를 제공합니다.
- **🔍 정밀한 이해**: PDF, Word, 이미지 등 문서의 구조화된 내용 추출을 지원하여 통일된 의미론적 뷰를 구축합니다.
- **🧠 지능형 추론**: 대규모 언어 모델을 활용하여 문서 문맥과 사용자 의도를 이해하고, 정확한 질의응답 및 다중 턴 대화를 지원합니다.
- **📚 다중 유형 지식 베이스**: FAQ 및 문서 두 가지 유형의 지식 베이스를 지원하며, 폴더 가져오기, URL 가져오기, 태그 관리 및 온라인 입력을 지원합니다.
- **🔧 유연한 확장**: 파싱, 임베딩, 리콜에서 생성까지 전체 프로세스가 분리되어 있어 유연한 통합 및 맞춤형 확장이 용이합니다.
- **⚡ 효율적 검색**: 키워드, 벡터, 지식 그래프 등 다양한 검색 전략을 혼합하고, 지식 베이스 간 검색을 지원합니다.
- **🌐 웹 검색**: 확장 가능한 웹 검색 엔진을 지원하며, DuckDuckGo 검색 엔진이 내장되어 있습니다.
- **🔌 MCP 도구 통합**: MCP를 통해 Agent 기능을 확장할 수 있으며, uvx 및 npx 실행 도구가 내장되어 있고 다양한 전송 방식을 지원합니다.
- **⚙️ 대화 전략**: Agent 모델, 일반 모드 모델, 검색 임계값 및 프롬프트를 구성할 수 있어 다중 턴 대화 동작을 정밀하게 제어할 수 있습니다.
- **🎯 간편한 사용**: 직관적인 웹 인터페이스와 표준 API를 제공하여 기술적 장벽 없이 빠르게 시작할 수 있습니다.
- **🔒 보안 및 제어**: 로컬 및 프라이빗 클라우드 배포를 지원하여 데이터 주권을 완전히 제어할 수 있습니다.

## 📊 적용 시나리오

| 적용 시나리오 | 구체적 애플리케이션 | 핵심 가치 |
|---------|----------|----------|
| **기업 지식 관리** | 내부 문서 검색, 규정 및 정책 질의응답, 운영 매뉴얼 조회 | 지식 탐색 효율성 향상, 교육 비용 절감 |
| **학술 연구 분석** | 논문 검색, 연구 보고서 분석, 학술 자료 정리 | 문헌 조사 가속화, 연구 의사결정 보조 |
| **제품 기술 지원** | 제품 매뉴얼 질의응답, 기술 문서 검색, 문제 해결 | 고객 서비스 품질 향상, 기술 지원 부담 감소 |
| **법률 및 규정 준수 검토** | 계약 조항 검색, 규제 정책 조회, 사례 분석 | 규정 준수 효율성 향상, 법적 위험 감소 |
| **의료 지식 보조** | 의학 문헌 검색, 진료 지침 조회, 임상 사례 분석 | 임상 의사결정 보조, 진료 품질 향상 |

## 🧩 기능 모듈

| 기능 모듈 | 지원 현황 | 설명 |
|---------|---------|------|
| Agent 모드 | ✅ ReACT Agent 모드 | 내장 도구를 사용하여 지식 베이스, MCP 도구 및 웹 검색을 검색하고, 지식 베이스 간 검색, 여러 번의 반복 및 반성을 지원합니다. |
| 지식 베이스 유형 | ✅ FAQ / 문서 | FAQ 및 문서 두 가지 유형의 지식 베이스 생성을 지원하며, 폴더 가져오기, URL 가져오기, 태그 관리 및 온라인 입력을 지원합니다. |
| 문서 형식 지원 | ✅ PDF / Word / Txt / Markdown / 이미지 (OCR / 캡션 포함) | 다양한 구조화 및 비구조화 문서 내용 파싱을 지원하며, 이미지와 텍스트 혼합 및 이미지 텍스트 추출을 지원합니다. |
| 모델 관리 | ✅ 중앙 집중식 구성, 내장 모델 공유 | 모델을 중앙에서 구성하고, 지식 베이스 설정 페이지에서 모델 선택을 추가하며, 다중 테넌트 공유 내장 모델을 지원합니다. |
| 임베딩 모델 지원 | ✅ 로컬 모델, BGE / GTE API 등 | 사용자 정의 임베딩 모델을 지원하며, 로컬 배포 및 클라우드 벡터 생성 인터페이스와 호환됩니다. |
| 벡터 데이터베이스 액세스 | ✅ PostgreSQL (pgvector), Elasticsearch | 주류 벡터 인덱스 백엔드를 지원하며, 다양한 검색 시나리오에 맞게 유연하게 전환 및 확장할 수 있습니다. |
| 검색 메커니즘 | ✅ BM25 / Dense Retrieve / GraphRAG | 밀집/희소 리콜, 지식 그래프 강화 검색 등 다양한 전략을 지원하며, 리콜-재정렬-생성 프로세스를 자유롭게 조합할 수 있습니다. |
| 대규모 모델 통합 | ✅ Qwen, DeepSeek 등 지원, 사고/비사고 모드 전환 | 로컬 대규모 모델(예: Ollama 시작)에 액세스하거나 외부 API 서비스를 호출할 수 있으며, 추론 모드를 유연하게 구성할 수 있습니다. |
| 대화 전략 | ✅ Agent 모델, 일반 모드 모델, 검색 임계값, 프롬프트 구성 | Agent 모델, 일반 모드에 필요한 모델, 검색 임계값을 구성하고, 온라인으로 프롬프트를 구성하여 다중 턴 대화 동작 및 검색 리콜 실행 방식을 정밀하게 제어할 수 있습니다. |
| 웹 검색 | ✅ 확장 가능한 검색 엔진, DuckDuckGo | 확장 가능한 웹 검색 엔진을 지원하며, DuckDuckGo 검색 엔진이 내장되어 있습니다. |
| MCP 도구 | ✅ uvx, npx 실행 도구, Stdio/HTTP Streamable/SSE | MCP를 통해 Agent 기능을 확장할 수 있으며, uvx 및 npx 두 가지 MCP 실행 도구가 내장되어 있고, 세 가지 전송 방식을 지원합니다. |
| 질의응답 기능 | ✅ 문맥 인식, 다중 턴 대화, 프롬프트 템플릿 | 복잡한 의미론적 모델링, 지침 제어 및 연쇄 질의응답을 지원하며, 프롬프트 및 문맥 창을 구성할 수 있습니다. |
| E2E 테스트 지원 | ✅ 검색+생성 과정 시각화 및 지표 평가 | 통합 링크 테스트 도구를 제공하여 리콜 적중률, 답변 커버리지, BLEU / ROUGE 등 주류 지표를 평가할 수 있습니다. |
| 배포 모드 | ✅ 로컬 배포 / Docker 이미지 지원 | 프라이빗, 오프라인 배포 및 유연한 운영 유지보수 요구 사항을 충족하며, 빠른 개발 모드를 지원합니다. |
| 사용자 인터페이스 | ✅ Web UI + RESTful API | 대화형 인터페이스 및 표준 API 인터페이스를 제공하며, Agent 모드/일반 모드 전환을 지원하고 도구 호출 과정을 표시합니다. |
| 작업 관리 | ✅ MQ 비동기 작업, 데이터베이스 자동 마이그레이션 | MQ를 도입하여 비동기 작업 상태를 유지하고, 버전 업그레이드 시 데이터베이스 테이블 구조 및 데이터 자동 마이그레이션을 지원합니다. |

## 🚀 시작하기

### 🛠 환경 요구 사항

로컬에 다음 도구가 설치되어 있는지 확인하십시오.

* [Docker](https://www.docker.com/)
* [Docker Compose](https://docs.docker.com/compose/)
* [Git](https://git-scm.com/)

### 📦 설치 단계

#### ① 코드 저장소 복제

```bash
# 메인 저장소 복제
git clone https://github.com/Tencent/WeKnora.git
cd WeKnora
```

#### ② 환경 변수 구성

```bash
# 예제 구성 파일 복사
cp .env.example .env

# .env 파일을 편집하고 해당 구성 정보를 입력하십시오.
# 모든 변수 설명은 .env.example 주석에 자세히 나와 있습니다.
```

#### ③ 서비스 시작 (Ollama 포함)

.env 파일에서 시작해야 하는 이미지를 확인하십시오.

```bash
./scripts/start_all.sh
```

또는

```bash
make start-all
```

#### ③.0 Ollama 서비스 시작 (선택 사항)

```bash
ollama serve > /dev/null 2>&1 &
```

#### ③.1 다양한 기능 조합 활성화

- 최소 기능 시작
```bash
docker compose up -d
```

- 모든 기능 시작
```bash
docker-compose --profile full up -d
```

- tracing 로그 필요
```bash
docker-compose --profile jaeger up -d
```

- neo4j 지식 그래프 필요
```bash
docker-compose --profile neo4j up -d
```

- minio 파일 저장 서비스 필요
```bash
docker-compose --profile minio up -d
```

- 다중 옵션 조합
```bash
docker-compose --profile neo4j --profile minio up -d
```

#### ④ 서비스 중지

```bash
./scripts/start_all.sh --stop
# 또는
make stop-all
```

### 🌐 서비스 액세스 주소

시작이 성공하면 다음 주소에 액세스할 수 있습니다.

* Web UI: `http://localhost`
* 백엔드 API: `http://localhost:8080`
* 링크 추적 (Jaeger): `http://localhost:16686`

### 🔌 WeChat 대화형 오픈 플랫폼 사용

WeKnora는 [WeChat 대화형 오픈 플랫폼](https://chatbot.weixin.qq.com)의 핵심 기술 프레임워크로서 더 간편한 사용 방식을 제공합니다.

- **노코드 배포**: 지식을 업로드하기만 하면 WeChat 생태계 내에서 지능형 질의응답 서비스를 빠르게 배포하여 "즉시 질문하고 즉시 답변하는" 경험을 실현할 수 있습니다.
- **효율적인 문제 관리**: 빈도가 높은 문제의 독립적인 분류 관리를 지원하고, 풍부한 데이터 도구를 제공하여 답변이 정확하고 신뢰할 수 있으며 유지 관리가 용이하도록 보장합니다.
- **WeChat 생태계 커버리지**: WeChat 대화형 오픈 플랫폼을 통해 WeKnora의 지능형 질의응답 기능을 공식 계정, 미니 프로그램 등 WeChat 시나리오에 원활하게 통합하여 사용자 상호 작용 경험을 향상시킬 수 있습니다.

### 🔗 MCP 서버를 사용하여 배포된 WeKnora 액세스

#### 1️⃣ 저장소 복제

```
git clone https://github.com/Tencent/WeKnora
```

#### 2️⃣ MCP 서버 구성

> [MCP 구성 설명](./mcp-server/MCP_CONFIG.md)을 직접 참조하여 구성하는 것을 권장합니다.

MCP 클라이언트에서 서버 구성
```json
{
  "mcpServers": {
    "weknora": {
      "args": [
        "path/to/WeKnora/mcp-server/run_server.py"
      ],
      "command": "python",
      "env":{
        "WEKNORA_API_KEY":"weknora 인스턴스에 들어가 개발자 도구를 열고 sk로 시작하는 요청 헤더 x-api-key를 확인하십시오.",
        "WEKNORA_BASE_URL":"http(s)://당신의_weknora_주소/api/v1"
      }
    }
  }
}
```

stdio 명령을 사용하여 직접 실행
```
pip install weknora-mcp-server
python -m weknora-mcp-server
```

## 🔧 초기화 구성 가이드

사용자가 다양한 모델을 빠르게 구성하고 시행착오 비용을 줄일 수 있도록 원래의 구성 파일 초기화 방식을 개선하여 Web UI 인터페이스를 추가하여 다양한 모델을 구성할 수 있도록 했습니다. 사용하기 전에 코드가 최신 버전으로 업데이트되었는지 확인하십시오. 구체적인 사용 단계는 다음과 같습니다.
이 프로젝트를 처음 사용하는 경우 ①② 단계를 건너뛰고 바로 ③④ 단계로 진행할 수 있습니다.

### ① 서비스 중지

```bash
./scripts/start_all.sh --stop
```

### ② 기존 데이터 테이블 지우기 (중요한 데이터가 없는 경우 권장)

```bash
make clean-db
```

### ③ 컴파일 및 서비스 시작

```bash
./scripts/start_all.sh
```

### ④ Web UI 액세스

http://localhost

처음 방문하면 자동으로 등록 및 로그인 페이지로 이동합니다. 등록을 완료한 후 새 지식 베이스를 만들고 해당 지식 베이스의 설정 페이지에서 관련 설정을 완료하십시오.

## 📱 기능 시연

### Web UI 인터페이스

<table>
  <tr>
    <td><b>지식 베이스 관리</b><br/><img src="./docs/images/knowledgebases.png" alt="지식 베이스 관리"></td>
    <td><b>대화 설정</b><br/><img src="./docs/images/settings.png" alt="대화 설정"></td>
  </tr>
  <tr>
    <td colspan="2"><b>Agent 모드 도구 호출 과정</b><br/><img src="./docs/images/agent-qa.png" alt="Agent 모드 도구 호출 과정"></td>
  </tr>
</table>

**지식 베이스 관리:** FAQ 및 문서 두 가지 유형의 지식 베이스 생성을 지원하며, 드래그 앤 드롭 업로드, 폴더 가져오기, URL 가져오기 등 다양한 방식을 지원합니다. 문서 구조를 자동으로 식별하고 핵심 지식을 추출하여 인덱스를 구축합니다. 태그 관리 및 온라인 입력을 지원하며, 시스템은 처리 진행 상황과 문서 상태를 명확하게 표시하여 효율적인 지식 베이스 관리를 실현합니다.

**Agent 모드:** ReACT Agent 모드 활성화를 지원하며, 내장 도구를 사용하여 지식 베이스를 검색하고, 사용자가 구성한 MCP 도구 및 웹 검색 도구를 호출하여 외부 서비스에 액세스할 수 있으며, 여러 번의 반복과 반성을 통해 최종적으로 포괄적인 요약 보고서를 제공합니다. 지식 베이스 간 검색을 지원하여 여러 지식 베이스를 선택하여 동시에 검색할 수 있습니다.

**대화 전략:** Agent 모델, 일반 모드에 필요한 모델, 검색 임계값을 구성하고, 온라인으로 프롬프트를 구성하여 다중 턴 대화 동작 및 검색 리콜 실행 방식을 정밀하게 제어할 수 있습니다. 대화 입력 상자는 Agent 모드/일반 모드 전환을 지원하고, 웹 검색 활성화 및 비활성화를 지원하며, 대화 모델 선택을 지원합니다.

### 문서 지식 그래프

WeKnora는 문서를 지식 그래프로 변환하여 문서의 서로 다른 단락 간의 연관 관계를 표시하는 것을 지원합니다. 지식 그래프 기능을 활성화하면 시스템은 문서 내부의 의미론적 연관 네트워크를 분석하고 구축하여 사용자가 문서 내용을 이해하는 데 도움을 줄 뿐만 아니라 인덱싱 및 검색을 위한 구조적 지원을 제공하여 검색 결과의 관련성과 범위를 향상시킵니다.

구체적인 구성은 [지식 그래프 구성 설명](./docs/KnowledgeGraph.md)을 참조하십시오.

### 관련 MCP 서버

관련 구성은 [MCP 구성 설명](./mcp-server/MCP_CONFIG.md)을 참조하십시오.

## 📘 문서

자주 묻는 질문 문제 해결: [자주 묻는 질문 문제 해결](./docs/QA.md)

자세한 인터페이스 설명은 다음을 참조하십시오: [API 문서](./docs/api/README.md)

## 🧭 개발 가이드

### ⚡ 빠른 개발 모드 (권장)

코드를 자주 수정해야 하는 경우, **매번 Docker 이미지를 다시 빌드할 필요가 없습니다**! 빠른 개발 모드를 사용하십시오.

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

**개발 이점:**
- ✅ 프론트엔드 수정 자동 핫 리로드 (재시작 필요 없음)
- ✅ 백엔드 수정 빠른 재시작 (5-10초, Air 핫 리로드 지원)
- ✅ Docker 이미지를 다시 빌드할 필요 없음
- ✅ IDE 중단점 디버깅 지원

**자세한 문서:** [개발 환경 빠른 시작](./docs/开发指南.md)

### 📁 프로젝트 디렉토리 구조

```
WeKnora/
├── client/      # go 클라이언트
├── cmd/         # 애플리케이션 진입점
├── config/      # 구성 파일
├── docker/      # docker 이미지 파일
├── docreader/   # 문서 파싱 프로젝트
├── docs/        # 프로젝트 문서
├── frontend/    # 프론트엔드 프로젝트
├── internal/    # 핵심 비즈니스 로직
├── mcp-server/  # MCP 서버
├── migrations/  # 데이터베이스 마이그레이션 스크립트
└── scripts/     # 시작 및 도구 스크립트
```

## 🤝 기여 가이드

커뮤니티 사용자의 기여를 환영합니다! 제안, 버그 또는 새로운 기능 요청이 있는 경우 [Issue](https://github.com/Tencent/WeKnora/issues)를 통해 제출하거나 직접 Pull Request를 제출하십시오.

### 🎯 기여 방식

- 🐛 **버그 수정**: 시스템 결함을 발견하고 수정
- ✨ **새로운 기능**: 새로운 기능 제안 및 구현
- 📚 **문서 개선**: 프로젝트 문서 개선
- 🧪 **테스트 케이스**: 단위 테스트 및 통합 테스트 작성
- 🎨 **UI/UX 최적화**: 사용자 인터페이스 및 경험 개선

### 📋 기여 프로세스

1. **프로젝트 Fork**하여 귀하의 GitHub 계정으로 복사
2. **기능 브랜치 생성** `git checkout -b feature/amazing-feature`
3. **변경 사항 커밋** `git commit -m 'Add amazing feature'`
4. **브랜치 푸시** `git push origin feature/amazing-feature`
5. **Pull Request 생성** 및 변경 내용 상세 설명

### 🎨 코드 규칙

- [Go Code Review Comments](https://github.com/golang/go/wiki/CodeReviewComments) 준수
- `gofmt`를 사용하여 코드 포맷팅
- 필요한 단위 테스트 추가
- 관련 문서 업데이트

### 📝 커밋 규칙

[Conventional Commits](https://www.conventionalcommits.org/) 규칙 사용:

```
feat: 문서 일괄 업로드 기능 추가
fix: 벡터 검색 정확도 문제 수정
docs: API 문서 업데이트
test: 검색 엔진 테스트 케이스 추가
refactor: 문서 파싱 모듈 리팩토링
```

## 👥 기여하신 분들

훌륭한 기여자분들께 감사드립니다:

[![Contributors](https://contrib.rocks/image?repo=Tencent/WeKnora)](https://github.com/Tencent/WeKnora/graphs/contributors)

## 📄 라이선스

이 프로젝트는 [MIT](./LICENSE) 라이선스에 따라 배포됩니다.
이 프로젝트의 코드를 자유롭게 사용, 수정 및 배포할 수 있지만 원본 저작권 표시를 유지해야 합니다.

## 📈 프로젝트 통계

<a href="https://www.star-history.com/#Tencent/WeKnora&type=date&legend=top-left">
 <picture>
   <source media="(prefers-color-scheme: dark)" srcset="https://api.star-history.com/svg?repos=Tencent/WeKnora&type=date&theme=dark&legend=top-left" />
   <source media="(prefers-color-scheme: light)" srcset="https://api.star-history.com/svg?repos=Tencent/WeKnora&type=date&legend=top-left" />
   <img alt="Star History Chart" src="https://api.star-history.com/svg?repos=Tencent/WeKnora&type=date&legend=top-left" />
 </picture>
</a>
