# QA 데이터셋 샘플링 도구

OpenAI의 GPT 모델을 사용하여 답변을 생성하는 포괄적인 QA 데이터셋 샘플링 도구입니다. 이 도구는 대규모 데이터셋(예: MS MARCO)에서 고품질의 질의응답 데이터셋을 생성하는 데 도움을 줍니다.

## 기능 특성

- **지능형 샘플링**: 대규모 데이터셋에서 쿼리, 문서 및 관련성 판단을 지능적으로 샘플링
- **답변 생성**: OpenAI의 GPT 모델을 사용하여 고품질 답변 자동 생성
- **중단점 이어하기**: 중단 후 생성 계속, 마지막 위치에서 시작 지원
- **진행 상황 추적**: 실시간 진행 상황 업데이트 및 통계 정보
- **결과 시각화**: 전체 문맥을 포함한 읽기 쉬운 질의응답 쌍 표시

## 설치 가이드

### 시스템 요구 사항

- Python 3.7+
- OpenAI API 키

### 종속성 설치

```bash
pip install pandas pyarrow openai
```

### 환경 변수 설정

```bash
export OPENAI_API_KEY="당신의-openai-api-key"
# 선택 사항: 사용자 정의 OpenAI 엔드포인트 사용
export OPENAI_BASE_URL="https://api.openai.com/v1"
```

### 데이터셋 준비

형식 요구 사항을 충족하는 모든 QA 데이터셋을 사용하거나 사전 처리된 샘플을 다운로드할 수 있습니다:

**HuggingFace/ModelScope 샘플 사용**
우리는 인기 있는 QA 데이터셋의 사전 처리된 샘플을 제공합니다:
- MarkrAI/eli5_sample_autorag
- MarkrAI/msmarco_sample_autorag
- MarkrAI/triviaqa_sample_autorag
- gnekt/hotpotqa_small_sample_autorag

**자체 데이터셋 사용**
데이터셋에 다음 파일이 포함되어 있는지 확인하십시오:
- `queries.parquet` (열: id, text)
- `corpus.parquet` (열: id, text)
- `qrels.parquet` (열: qid, pid)

## 빠른 시작

### 1. 대규모 데이터셋에서 샘플링

먼저 전체 데이터셋에서 쿼리, 문서 및 관련성 판단의 하위 집합을 샘플링합니다:

```bash
python dataset/qa_dataset.py sample \
  --queries ~/dataset/mmarco-queries.parquet \
  --corpus ~/dataset/mmarco-corpus.parquet \
  --qrels ~/dataset/mmarco-qrels.parquet \
  --nq 100 \
  --output_dir ./dataset/samples
```

### 2. 답변 생성

OpenAI의 GPT 모델을 사용하여 샘플링된 질의응답에 대한 답변을 생성합니다:

```bash
python dataset/qa_dataset.py generate \
  --input_dir ./dataset/samples \
  --output_dir ./dataset/samples
```

### 3. 결과 확인

생성된 질의응답 쌍과 그 문맥을 표시합니다:

```bash
python dataset/qa_dataset.py show \
  --input_dir ./dataset/samples \
  -n 5
```

## 상세 사용 설명

### 샘플링 명령

전체 데이터셋에서 대표 샘플을 생성합니다.

```bash
python dataset/qa_dataset.py sample [옵션]
```

**필수 매개변수:**
- `--queries`: 쿼리 parquet 파일 경로 (열: `id`, `text`)
- `--corpus`: 말뭉치 parquet 파일 경로 (열: `id`, `text`)
- `--qrels`: 관련성 판단 parquet 파일 경로 (열: `qid`, `pid`)

**선택 매개변수:**
- `--nq`: 샘플링할 쿼리 수 (기본값: 1000)
- `--output_dir`: 샘플 데이터 출력 디렉토리 (기본값: ./save)

**예시:**
```bash
python dataset/qa_dataset.py sample \
  --queries data/queries.parquet \
  --corpus data/corpus.parquet \
  --qrels data/qrels.parquet \
  --nq 500 \
  --output_dir ./my_sample
```

### 생성 명령

OpenAI API를 사용하여 샘플 질문에 대한 답변을 생성합니다.

```bash
python dataset/qa_dataset.py generate [옵션]
```

**필수 매개변수:**
- `--input_dir`: 샘플 데이터가 포함된 디렉토리 (queries.parquet, corpus.parquet, qrels.parquet)

**선택 매개변수:**
- `--output_dir`: 생성된 답변의 출력 디렉토리 (기본값: ./save)

**특성:**
- **중단점 이어하기**: 중단 후 자동으로 마지막 위치에서 계속
- **오류 처리**: API 호출 실패 시 3회 자동 재시도
- **진행 상황 저장**: 답변이 성공적으로 생성될 때마다 진행 상황 저장

**예시:**
```bash
python dataset/qa_dataset.py generate \
  --input_dir ./my_sample \
  --output_dir ./my_sample
```

### 표시 명령

생성된 질의응답 쌍 및 전체 문맥을 표시합니다.

```bash
python dataset/qa_dataset.py show [옵션]
```

**필수 매개변수:**
- `--input_dir`: QA 데이터가 포함된 디렉토리 (queries.parquet, corpus.parquet, qrels.parquet, qas.parquet, answers.parquet)

**선택 매개변수:**
- `-n`: 표시할 결과 수 (기본값: 5)

**예시:**
```bash
python dataset/qa_dataset.py show \
  --input_dir ./my_sample \
  -n 3
```

## 입력 데이터 형식

### 쿼리 파일 (queries.parquet)
| 열 이름 | 유형 | 설명 |
|------|------|------|
| id | string | 고유 쿼리 식별자 |
| text | string | 실제 질문 텍스트 |

### 말뭉치 파일 (corpus.parquet)
| 열 이름 | 유형 | 설명 |
|------|------|------|
| id | string | 고유 단락/문서 식별자 |
| text | string | 단락/문서 내용 |

### 관련성 판단 파일 (qrels.parquet)
| 열 이름 | 유형 | 설명 |
|------|------|------|
| qid | string | 쿼리 ID (queries.id와 일치) |
| pid | string | 단락 ID (corpus.id와 일치) |

## 출력 파일

모든 명령을 실행한 후 출력 디렉토리에 다음이 포함됩니다:

### 샘플링 데이터
- `queries.parquet`: 샘플링된 쿼리 하위 집합
- `corpus.parquet`: 샘플링된 문서 하위 집합
- `qrels.parquet`: 샘플링된 관련성 판단

### 생성된 답변
- `answers.parquet`: 생성된 답변 (고유 ID 포함)
- `qas.parquet`: 질의응답 매핑 (qid → aid)

## 고급 사용법

### 사용자 정의 OpenAI 구성

다른 OpenAI 모델이나 엔드포인트를 사용할 수 있습니다:

```bash
# GPT-4 Turbo 사용
export OPENAI_API_KEY="당신의-키"
python dataset/qa_dataset.py generate --input_dir ./samples

# Azure OpenAI 사용
export OPENAI_API_KEY="azure-키"
export OPENAI_BASE_URL="https://당신의-리소스.openai.azure.com/openai/deployments/gpt-4"
python dataset/qa_dataset.py generate --input_dir ./samples
```

### 대규모 데이터셋 샘플링

매우 큰 데이터셋의 경우 일괄 샘플링을 권장합니다:

```bash
# 첫 번째 배치
python dataset/qa_dataset.py sample --nq 1000 --output_dir ./batch1
python dataset/qa_dataset.py generate --input_dir ./batch1

# 두 번째 배치
python dataset/qa_dataset.py sample --nq 1000 --output_dir ./batch2
python dataset/qa_dataset.py generate --input_dir ./batch2
```

## 문제 해결

### 자주 묻는 질문

**1. OpenAI API 오류**
- API 키가 올바르게 설정되었는지 확인: `echo $OPENAI_API_KEY`
- API 할당량 및 청구 상태 확인
- OpenAI와의 네트워크 연결 확인

**2. 대규모 데이터셋 메모리 문제**
- `--nq` 매개변수를 줄여 더 작은 샘플 획득
- pandas 작업에 충분한 RAM이 있는지 확인
- 더 작은 parquet 파일 사용 고려

**3. 파일을 찾을 수 없음 오류**
- 모든 입력 파일 경로가 올바른지 확인
- parquet 파일에 올바른 열 이름이 있는지 확인
- 파일 권한 확인

### 디버그 모드

print 문을 추가하거나 Python 디버거를 사용하여 상세 출력을 활성화합니다:

```bash
python -m pdb dataset/qa_dataset.py sample --queries ...
```

## 예시 워크플로우

```bash
# 1. 환경 설정
export OPENAI_API_KEY="sk-..."

# 2. MS MARCO에서 200개 쿼리 샘플링
python dataset/qa_dataset.py sample \
  --queries ~/mmarco/queries.parquet \
  --corpus ~/mmarco/corpus.parquet \
  --qrels ~/mmarco/qrels.parquet \
  --nq 200 \
  --output_dir ./marco_sample

# 3. 답변 생성 (API 속도 제한에 따라 시간이 걸릴 수 있음)
python dataset/qa_dataset.py generate \
  --input_dir ./marco_sample \
  --output_dir ./marco_sample

# 4. 결과 확인
python dataset/qa_dataset.py show \
  --input_dir ./marco_sample \
  -n 10
```

## 기여

문제 및 기능 향상 요청 제출을 환영합니다!

## 라이선스

MIT 라이선스 - 연구 및 프로젝트에 자유롭게 사용할 수 있습니다.
