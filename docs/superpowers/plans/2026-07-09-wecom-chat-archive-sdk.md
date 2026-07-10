# WeCom Chat Archive SDK Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the unavailable default WeCom chat archive client with a real Linux amd64 CGO adapter for the official enterprise WeChat archive C SDK at `sdk_x86_v3_20250205/C_sdk`.

**Architecture:** Keep the existing `ArchiveClient` interface as the connector boundary. Add a build-tagged Linux amd64 implementation that calls `libWeWorkFinanceSdk_C.so` through CGO, translates SDK JSON into `ArchiveMessageEnvelope`, decrypts message payloads, and keeps non-Linux/non-CGO builds on the current unavailable client.

**Tech Stack:** Go 1.26, CGO, official WeCom C SDK `20250205`, Docker Debian bookworm, WeKnora datasource connector tests with fake SDK seams.

## Global Constraints

- SDK path is `sdk_x86_v3_20250205/C_sdk`.
- C header is `sdk_x86_v3_20250205/C_sdk/WeWorkFinanceSdk_C.h`.
- Runtime library is `sdk_x86_v3_20250205/C_sdk/libWeWorkFinanceSdk_C.so`.
- SDK version file contains `20250205`.
- Production target is Linux amd64 only for the real SDK client.
- Non-Linux or non-CGO builds must continue to compile and return the existing clear SDK-not-configured error.
- Keep the connector-facing `ArchiveClient` interface stable unless a task explicitly updates all callers and tests.
- Do not parse or download attachments in this SDK phase.
- Do not implement document-level ACL enforcement in this SDK phase.
- Do not use `FetchedItem.IsDeleted` for revoke messages.
- Do not log `secret`, `private_key`, message decryption keys, encrypted payloads, decrypted chat body, or full SDK response body.
- All SDK/client errors returned to WeKnora must pass through the existing redaction boundary.
- Use TDD: write failing tests, verify red, implement, verify green.

---

## File Structure

Create:

- `internal/datasource/connector/wecom_chat_archive/sdk_json.go`: pure-Go JSON models and conversion from SDK `GetChatData` / decrypted message payloads into `ArchiveMessageEnvelope`.
- `internal/datasource/connector/wecom_chat_archive/sdk_json_test.go`: tests for SDK JSON parsing, decrypted payload conversion, sender extraction, message type fields, and pagination `hasMore` inference.
- `internal/datasource/connector/wecom_chat_archive/client_linux_amd64.go`: Linux amd64 CGO implementation of `ArchiveClient` using `WeWorkFinanceSdk_C.h` and `libWeWorkFinanceSdk_C.so`.
- `internal/datasource/connector/wecom_chat_archive/client_unavailable.go`: fallback implementation for non-Linux/non-CGO builds.
- `internal/datasource/connector/wecom_chat_archive/client_linux_amd64_test.go`: build-tagged tests for client construction and fake C-call seam behavior where possible.

Modify:

- `internal/datasource/connector/wecom_chat_archive/client.go`: keep only the interface, `clientFactory`, and shared constructor declarations that are build-neutral.
- `internal/datasource/connector/wecom_chat_archive/types.go`: add optional client settings for `proxy`, `proxy_password`, `timeout_seconds` if needed by SDK calls.
- `internal/datasource/connector/wecom_chat_archive/markdown.go`: use parsed decrypted payload text/link/news fields instead of storing whole raw decrypted JSON.
- `internal/datasource/connector/wecom_chat_archive/connector_test.go`: extend tests to cover parsed payload behavior, not just raw string behavior.
- `docker/Dockerfile.app`: copy SDK `.so` into builder and final image and configure runtime linker path.
- `.dockerignore` if it excludes `sdk_x86_v3_20250205`; ensure SDK directory is available to Docker build context only if the binary is intended to be packaged.
- `docs/wecom-chat-archive-datasource-design.md`: document real SDK packaging, Linux amd64 constraint, and how to provide SDK files.

---

### Task 1: SDK JSON Parsing And Message Conversion

**Files:**
- Create: `internal/datasource/connector/wecom_chat_archive/sdk_json.go`
- Create: `internal/datasource/connector/wecom_chat_archive/sdk_json_test.go`
- Modify: `internal/datasource/connector/wecom_chat_archive/markdown.go`
- Modify: `internal/datasource/connector/wecom_chat_archive/connector_test.go`

**Interfaces:**
- Consumes: decrypted SDK JSON strings and `ArchiveMessageEnvelope`
- Produces: `parseChatDataResponse(data []byte) ([]sdkChatData, bool, error)`, `decodeDecryptedMessage(seq uint64, msgID string, payload []byte) (ArchiveMessageEnvelope, error)`, normalized raw text fields for Markdown rendering

- [ ] **Step 1: Write the failing tests**

Create `sdk_json_test.go` with tests for:

```go
func TestParseChatDataResponseReadsChatData(t *testing.T) {
	raw := []byte(`{"errcode":0,"errmsg":"ok","chatdata":[{"seq":196,"msgid":"m1","publickey_ver":3,"encrypt_random_key":"k","encrypt_chat_msg":"c"}]}`)
	items, hasMore, err := parseChatDataResponse(raw)
	if err != nil {
		t.Fatalf("parseChatDataResponse error: %v", err)
	}
	if len(items) != 1 || items[0].Seq != 196 || items[0].MsgID != "m1" {
		t.Fatalf("items = %#v", items)
	}
	if hasMore {
		t.Fatal("hasMore should be false when returned item count is below limit in caller")
	}
}

func TestParseChatDataResponseReturnsSDKError(t *testing.T) {
	raw := []byte(`{"errcode":10009,"errmsg":"ip invalid"}`)
	_, _, err := parseChatDataResponse(raw)
	if err == nil || !strings.Contains(err.Error(), "10009") {
		t.Fatalf("error = %v", err)
	}
}

func TestDecodeTextMessage(t *testing.T) {
	payload := []byte(`{"msgid":"m1","action":"send","from":"zhangsan","tolist":["lisi"],"roomid":"wr_xxx","msgtime":1783405282000,"msgtype":"text","text":{"content":"hello"}}`)
	msg, err := decodeDecryptedMessage(196, "m1", payload)
	if err != nil {
		t.Fatalf("decodeDecryptedMessage error: %v", err)
	}
	if msg.Seq != 196 || msg.MsgID != "m1" || msg.MsgType != "text" {
		t.Fatalf("msg = %#v", msg)
	}
	if msg.ConversationID != "wr_xxx" || msg.ConversationType != conversationTypeRoom {
		t.Fatalf("conversation = %q/%q", msg.ConversationID, msg.ConversationType)
	}
	if msg.From.UserID != "zhangsan" || msg.ToList[0].UserID != "lisi" {
		t.Fatalf("senders = %#v -> %#v", msg.From, msg.ToList)
	}
	if string(msg.Raw) != "hello" {
		t.Fatalf("Raw = %q, want text body", string(msg.Raw))
	}
}

func TestDecodeExternalSingleConversation(t *testing.T) {
	payload := []byte(`{"msgid":"m2","action":"send","from":"wm_ext","tolist":["zhangsan"],"msgtime":1783405282000,"msgtype":"text","text":{"content":"external hello"}}`)
	msg, err := decodeDecryptedMessage(197, "m2", payload)
	if err != nil {
		t.Fatalf("decodeDecryptedMessage error: %v", err)
	}
	if msg.ConversationType != conversationTypeSingle {
		t.Fatalf("ConversationType = %q", msg.ConversationType)
	}
	if msg.ConversationID == "" {
		t.Fatal("ConversationID should be stable for single chat")
	}
	if msg.From.ExternalUserID != "wm_ext" {
		t.Fatalf("From = %#v", msg.From)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/datasource/connector/wecom_chat_archive -run 'TestParseChatDataResponse|TestDecode' -count=1`

Expected: FAIL because parser functions do not exist.

- [ ] **Step 3: Implement pure-Go SDK JSON parser**

Implement `sdk_json.go` with:

- SDK response struct: `errcode`, `errmsg`, `chatdata`.
- Chat data struct: `seq`, `msgid`, `publickey_ver`, `encrypt_random_key`, `encrypt_chat_msg`.
- Decrypted message struct for common fields: `msgid`, `action`, `from`, `tolist`, `roomid`, `msgtime`, `msgtype`, plus typed sections `text`, `markdown`, `link`, `news`, `mixed`.
- `parseChatDataResponse` must return an error when `errcode != 0`.
- `decodeDecryptedMessage` must set `Raw` to normalized content text/summary for supported message types, not the full decrypted JSON.
- `ConversationID` rules: room message uses `roomid`; single chat uses stable sorted pair key from `from` and first `tolist`, prefixed by `single:`.
- Sender classification: ids starting `wm` or `wo` become external; non-empty non-external ids become internal; empty becomes unknown.

- [ ] **Step 4: Update renderer tests if existing raw-string assumptions conflict**

If current tests pass because `Raw` is already normalized text, do not change them. If they assume raw decrypted JSON, update them to use normalized content.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/datasource/connector/wecom_chat_archive -run 'TestParseChatDataResponse|TestDecode|TestBucketItem|TestNormalizeMessage' -count=1`

Expected: PASS.

- [ ] **Step 6: Commit**

Run:

```bash
git add internal/datasource/connector/wecom_chat_archive/sdk_json.go internal/datasource/connector/wecom_chat_archive/sdk_json_test.go internal/datasource/connector/wecom_chat_archive/markdown.go internal/datasource/connector/wecom_chat_archive/connector_test.go
git commit -m "feat(datasource): parse wecom archive sdk messages"
```

---

### Task 2: Build-Neutral Client Split

**Files:**
- Modify: `internal/datasource/connector/wecom_chat_archive/client.go`
- Create: `internal/datasource/connector/wecom_chat_archive/client_unavailable.go`
- Test: `internal/datasource/connector/wecom_chat_archive/connector_test.go`

**Interfaces:**
- Consumes: existing `ArchiveClient` and `newUnavailableClient`
- Produces: build-neutral `client.go` and fallback client behind `!linux || !amd64 || !cgo` build tags

- [ ] **Step 1: Write/keep failing-safe test**

Add a test that default connector still returns SDK-not-configured when no real SDK client is available in test builds:

```go
func TestDefaultClientReportsSDKUnavailable(t *testing.T) {
	err := NewConnector().Validate(context.Background(), validConfig())
	if err == nil || !strings.Contains(err.Error(), "SDK client is not configured") {
		t.Fatalf("Validate error = %v", err)
	}
}
```

- [ ] **Step 2: Run test before split**

Run: `go test ./internal/datasource/connector/wecom_chat_archive -run TestDefaultClientReportsSDKUnavailable -count=1`

Expected: PASS before split. This pins fallback behavior.

- [ ] **Step 3: Split fallback client into build-tagged file**

Update `client.go` to contain only:

```go
package wecom_chat_archive

import "context"

type ArchiveClient interface {
	Validate(ctx context.Context) error
	FetchMessages(ctx context.Context, startSeq uint64, limit int) ([]ArchiveMessageEnvelope, bool, error)
	Close() error
}

type clientFactory func(cfg *Config) ArchiveClient
```

Create `client_unavailable.go`:

```go
//go:build !linux || !amd64 || !cgo

package wecom_chat_archive

import (
	"context"
	"fmt"
)

func newUnavailableClient(cfg *Config) ArchiveClient {
	return unavailableClient{}
}

type unavailableClient struct{}

func (unavailableClient) Validate(ctx context.Context) error {
	return fmt.Errorf("wecom chat archive SDK client is not configured in this build")
}

func (unavailableClient) FetchMessages(ctx context.Context, startSeq uint64, limit int) ([]ArchiveMessageEnvelope, bool, error) {
	return nil, false, fmt.Errorf("wecom chat archive SDK client is not configured in this build")
}

func (unavailableClient) Close() error { return nil }
```

- [ ] **Step 4: Run tests with CGO disabled**

Run: `CGO_ENABLED=0 go test ./internal/datasource/connector/wecom_chat_archive -run TestDefaultClientReportsSDKUnavailable -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

Run:

```bash
git add internal/datasource/connector/wecom_chat_archive/client.go internal/datasource/connector/wecom_chat_archive/client_unavailable.go internal/datasource/connector/wecom_chat_archive/connector_test.go
git commit -m "refactor(datasource): split wecom archive client by build target"
```

---

### Task 3: Linux amd64 CGO SDK Client

**Files:**
- Create: `internal/datasource/connector/wecom_chat_archive/client_linux_amd64.go`
- Create: `internal/datasource/connector/wecom_chat_archive/client_linux_amd64_test.go`
- Modify: `internal/datasource/connector/wecom_chat_archive/types.go`

**Interfaces:**
- Consumes: official SDK functions `NewSdk`, `Init`, `GetChatData`, `DecryptData`, `DestroySdk`, `NewSlice`, `FreeSlice`
- Produces: real `newUnavailableClient(cfg *Config) ArchiveClient` for `linux && amd64 && cgo`, implemented by `financeSDKClient`

- [ ] **Step 1: Add SDK settings tests**

Add config parsing tests for optional settings:

```go
func TestParseConfigReadsSDKNetworkSettings(t *testing.T) {
	cfg := validConfig()
	cfg.Settings["proxy"] = "socks5://127.0.0.1:8081"
	cfg.Settings["proxy_password"] = "user:pass"
	cfg.Settings["timeout_seconds"] = float64(7)
	parsed, err := parseConfig(cfg)
	if err != nil {
		t.Fatalf("parseConfig error: %v", err)
	}
	if parsed.Settings.Proxy != "socks5://127.0.0.1:8081" || parsed.Settings.ProxyPassword != "user:pass" || parsed.Settings.TimeoutSeconds != 7 {
		t.Fatalf("settings = %#v", parsed.Settings)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/datasource/connector/wecom_chat_archive -run TestParseConfigReadsSDKNetworkSettings -count=1`

Expected: FAIL because fields do not exist.

- [ ] **Step 3: Add SDK network settings**

Add to `Settings`:

```go
	Proxy          string `json:"proxy"`
	ProxyPassword  string `json:"proxy_password"`
	TimeoutSeconds int    `json:"timeout_seconds"`
```

Set default timeout to 5 seconds in `defaultSettings` and `applyDefaults`:

```go
	TimeoutSeconds: 5,
```

```go
	if s.TimeoutSeconds <= 0 {
		s.TimeoutSeconds = 5
	}
```

- [ ] **Step 4: Create CGO client**

Create `client_linux_amd64.go` with build tag:

```go
//go:build linux && amd64 && cgo
```

Use CGO directives:

```go
/*
#cgo CFLAGS: -I${SRCDIR}/../../../../sdk_x86_v3_20250205/C_sdk
#cgo LDFLAGS: -L${SRCDIR}/../../../../sdk_x86_v3_20250205/C_sdk -lWeWorkFinanceSdk_C -Wl,-rpath,$ORIGIN
#include <stdlib.h>
#include "WeWorkFinanceSdk_C.h"
*/
import "C"
```

Implement:

- `type financeSDKClient struct { cfg *Config; sdk *C.WeWorkFinanceSdk_t }`
- `newUnavailableClient(cfg *Config) ArchiveClient` returns `newFinanceSDKClient(cfg)` for this build target.
- `Validate(ctx)` initializes SDK and fetches at most one message from seq 0 with limit 1.
- `FetchMessages(ctx, startSeq, limit)` clamps limit to `1..1000`, calls `GetChatData`, parses response, decrypts each item with `DecryptData`, decodes decrypted payload via `decodeDecryptedMessage`, returns `hasMore = len(items) >= limit`.
- `Close()` calls `DestroySdk` once when initialized.

Every C allocation must be freed:

- `C.CString` with `C.free`.
- SDK slices with `C.FreeSlice`.
- SDK handle with `C.DestroySdk`.

Do not include raw SDK JSON or decrypted body in returned errors. Use concise errors with SDK return code and `sanitizeConnectorError` at connector boundary.

- [ ] **Step 5: Add Linux amd64 compile test**

Create `client_linux_amd64_test.go` with build tag `linux && amd64 && cgo`:

```go
//go:build linux && amd64 && cgo

package wecom_chat_archive

import "testing"

func TestLinuxClientFactoryReturnsArchiveClient(t *testing.T) {
	client := newUnavailableClient(&Config{})
	if client == nil {
		t.Fatal("client is nil")
	}
	_ = client.Close()
}
```

- [ ] **Step 6: Run tests**

On macOS, run fallback compile:

Run: `CGO_ENABLED=0 go test ./internal/datasource/connector/wecom_chat_archive -count=1`

Expected: PASS.

On Linux amd64, run:

Run: `CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go test ./internal/datasource/connector/wecom_chat_archive -run TestLinuxClientFactoryReturnsArchiveClient -count=1`

Expected: PASS when SDK headers and `.so` are present.

If cross-running Linux CGO tests is not possible on macOS, run Docker build in Task 4 as the Linux compile gate.

- [ ] **Step 7: Commit**

Run:

```bash
git add internal/datasource/connector/wecom_chat_archive/client_linux_amd64.go internal/datasource/connector/wecom_chat_archive/client_linux_amd64_test.go internal/datasource/connector/wecom_chat_archive/types.go internal/datasource/connector/wecom_chat_archive/connector_test.go
git commit -m "feat(datasource): add wecom archive c sdk client"
```

---

### Task 4: Docker Packaging For SDK Library

**Files:**
- Modify: `docker/Dockerfile.app`
- Modify: `.dockerignore` if needed
- Test: Docker build command

**Interfaces:**
- Consumes: SDK files under `sdk_x86_v3_20250205/C_sdk`
- Produces: final app image with `libWeWorkFinanceSdk_C.so` available to the app binary at runtime

- [ ] **Step 1: Check docker context includes SDK files**

Run: `git check-ignore -v sdk_x86_v3_20250205/C_sdk/libWeWorkFinanceSdk_C.so || true`

Expected: no ignore rule blocks the SDK directory. The SDK directory is locally ignored via `.git/info/exclude` and must not be committed. If `.dockerignore` blocks the SDK directory, update `.dockerignore` with an exception:

```text
!sdk_x86_v3_20250205/
!sdk_x86_v3_20250205/C_sdk/
!sdk_x86_v3_20250205/C_sdk/WeWorkFinanceSdk_C.h
!sdk_x86_v3_20250205/C_sdk/libWeWorkFinanceSdk_C.so
```

- [ ] **Step 2: Modify Dockerfile builder stage**

After `COPY . .`, add:

```dockerfile
RUN test -f sdk_x86_v3_20250205/C_sdk/WeWorkFinanceSdk_C.h && \
    test -f sdk_x86_v3_20250205/C_sdk/libWeWorkFinanceSdk_C.so
```

This fails early if SDK files are absent.

- [ ] **Step 3: Copy SDK library into final image**

Before copying the app binary in final stage, add:

```dockerfile
COPY --from=builder /app/sdk_x86_v3_20250205/C_sdk/libWeWorkFinanceSdk_C.so /usr/local/lib/libWeWorkFinanceSdk_C.so
RUN ldconfig
```

If `ldconfig` is unavailable, add package `libc-bin` to the final stage apt install list or set `ENV LD_LIBRARY_PATH=/usr/local/lib:${LD_LIBRARY_PATH}`.

- [ ] **Step 4: Build image**

Run: `docker build -f docker/Dockerfile.app -t weknora-app-wecom-sdk-test .`

Expected: build reaches `make build-prod` and final image creation without missing SDK header/library errors.

- [ ] **Step 5: Runtime library smoke**

Run: `docker run --rm weknora-app-wecom-sdk-test sh -lc 'test -f /usr/local/lib/libWeWorkFinanceSdk_C.so && ldd /app/WeKnora | grep -E "WeWorkFinanceSdk|not found" || true'`

Expected: library file exists; no `not found` for `libWeWorkFinanceSdk_C.so`.

- [ ] **Step 6: Commit**

Run:

```bash
git add docker/Dockerfile.app .dockerignore
git commit -m "build(docker): package wecom finance sdk library"
```

---

### Task 5: SDK Documentation And Operational Notes

**Files:**
- Modify: `docs/wecom-chat-archive-datasource-design.md`
- Create: `docs/wecom-chat-archive-sdk.md`

**Interfaces:**
- Consumes: implemented SDK adapter and Docker packaging behavior
- Produces: operator-facing instructions for SDK files, Linux amd64 constraint, local validation, Docker build, and troubleshooting

- [ ] **Step 1: Write documentation**

Create `docs/wecom-chat-archive-sdk.md`:

```markdown
# 企业微信会话存档 SDK 接入说明

## SDK 版本

- SDK 目录：`sdk_x86_v3_20250205/C_sdk`
- SDK 版本：`20250205`
- 头文件：`WeWorkFinanceSdk_C.h`
- 动态库：`libWeWorkFinanceSdk_C.so`
- MVP 生产目标：Linux amd64

## 本地构建

非 Linux 或 `CGO_ENABLED=0` 构建会使用 unavailable client，并返回 `wecom chat archive SDK client is not configured in this build`。

Linux amd64 构建需要：

```bash
CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build ./cmd/server
```

## Docker 构建

```bash
docker build -f docker/Dockerfile.app -t weknora-app-wecom-sdk .
```

## 运行要求

容器内必须能加载：

```text
/usr/local/lib/libWeWorkFinanceSdk_C.so
```

## 故障排查

- `SDK client is not configured`: 当前构建不是 Linux amd64 CGO 构建，或 SDK 文件未进入构建上下文。
- `Init ret != 0`: 检查 `corp_id`、`secret` 和企业微信会话存档授权。
- `GetChatData ret != 0`: 检查网络、IP 白名单、代理配置和 SDK 返回码。
- `DecryptData ret != 0`: 检查私钥版本、私钥内容和 `encrypt_random_key`。

日志不得输出 `secret`、`private_key`、解密密钥、加密消息体或解密正文。
```

Update `docs/wecom-chat-archive-datasource-design.md` with a short section linking to this SDK doc.

- [ ] **Step 2: Run markdown sanity check**

Run: `grep -R "TBD\|TODO" docs/wecom-chat-archive-sdk.md docs/wecom-chat-archive-datasource-design.md`

Expected: no output.

- [ ] **Step 3: Commit**

Run:

```bash
git add docs/wecom-chat-archive-sdk.md docs/wecom-chat-archive-datasource-design.md
git commit -m "docs: document wecom archive sdk setup"
```

---

### Task 6: Final Verification

**Files:**
- Verify only; no planned edits.

**Interfaces:**
- Consumes: SDK parsing, CGO client, Docker packaging, docs.
- Produces: verified branch state.

- [ ] **Step 1: Run pure Go connector tests**

Run: `CGO_ENABLED=0 go test ./internal/datasource/connector/wecom_chat_archive -count=1`

Expected: PASS.

- [ ] **Step 2: Run focused backend tests**

Run: `go test ./internal/datasource ./internal/datasource/connector/wecom_chat_archive ./internal/types -count=1`

Expected: PASS.

- [ ] **Step 3: Run frontend regression checks from prior phase**

Run: `npm --prefix frontend test -- src/views/knowledge/settings/datasourceFieldRendering.test.ts src/views/knowledge/settings/datasourceDefaults.test.ts`

Expected: PASS.

Run: `npm --prefix frontend run type-check`

Expected: PASS.

- [ ] **Step 4: Run Docker SDK build check**

Run: `docker build -f docker/Dockerfile.app -t weknora-app-wecom-sdk-test .`

Expected: PASS.

- [ ] **Step 5: Run runtime library smoke**

Run: `docker run --rm weknora-app-wecom-sdk-test sh -lc 'test -f /usr/local/lib/libWeWorkFinanceSdk_C.so && ldd /app/WeKnora | grep -E "WeWorkFinanceSdk|not found" || true'`

Expected: library file exists; no `not found` output.

- [ ] **Step 6: Check git status**

Run: `git status --short`

Expected: no uncommitted changes except intentionally ignored scratch files.

---

## Self-Review Notes

- Spec coverage: this plan implements the real official C SDK adapter, JSON message conversion, Docker packaging, and operational documentation. It intentionally excludes attachment download/parsing, document-level ACL enforcement, and real WeCom integration tests requiring live credentials.
- Placeholder scan: no `TBD`, `TODO`, or unspecified implementation steps remain.
- Type consistency: the existing `ArchiveClient` interface remains stable; `ArchiveMessageEnvelope.Raw` becomes normalized content bytes for supported message types; fallback builds continue through build tags.
