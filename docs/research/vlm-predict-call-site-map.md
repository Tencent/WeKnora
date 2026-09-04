# VLM Predict Call-Site Map

## 1. All `Predict` callers (repo-wide `\.Predict\(`)

| # | File:line | Shape | Notes |
|---|-----------|-------|-------|
| 1 | `internal/application/service/image_multimodal.go:271` | `vlmModel.Predict(ctx, [][]byte{imgBytes}, prompt)` — OCR | Only when `payload.EnableOCR`; prompt = `vlmOCRPrompt` or `vlmOCRScannedPDFPrompt` + `AppendCustomPromptInstructions(..., "image_ocr")`. Errors logged to `imgOut["ocr_error"]`, empty/sanitized-empty discarded. |
| 2 | `internal/application/service/image_multimodal.go:289` | `vlmModel.Predict(ctx, [][]byte{imgBytes}, buildVLMCaptionPrompt(ctx, vlmCfg))` — caption | UNCONDITIONAL (runs even when OCR disabled / even if OCR succeeded). Single image per call. |
| 3 | `internal/application/service/temporary_document.go:536` | `model.Predict(ctx, [][]byte{img}, ocrPrompt)` — page OCR | Inside `understandImagesWithVLM`; bounded-concurrency fan-out (`sem`, `temporaryDocumentOCRConcurrency()`), one goroutine per page, results reassembled in page order. Prompt reuses `vlmOCRPrompt` / `vlmOCRScannedPDFPrompt` (imported from image_multimodal.go). |
| 4 | `internal/application/service/temporary_document.go:560` | `model.Predict(ctx, [][]byte{images[0]}, buildVLMCaptionPrompt(ctx, types.VLMConfig{}))` — caption fallback | Only if `captionFallback && ocrRunes < temporaryDocumentOCRSufficientRunes`; single caption over `images[0]` prepended to OCR parts. Note empty `VLMConfig{}` → language falls back to `LanguageNameFromContext`. |
| 5 | `internal/handler/session/image_upload.go:86` | `vlmModel.Predict(ctx, [][]byte{imgBytes}, prompt)` — chat fallback analysis | In `analyzeImageAttachments`; per-image loop over `decodeDataURI(img.Data)` bytes; prompt = `buildImageAnalysisPrompt(userQuery)` (single query-aware call, NOT OCR+caption split). Best-effort; pure-chat path only. |
| 6 | `internal/handler/model.go:512` | `instance.Predict(ctx, [][]byte{fileBytes}, input)` — model debug/test-connection | `ModelTypeVLLM` branch of debug endpoint; `fileBytes` = uploaded image file. |
| 7 | `internal/application/service/agent_service.go:243` | `vlmModel.Predict(ctx, [][]byte{imgBytes}, prompt)` via `engine.SetImageDescriber(...)` closure | MCP tool-result image describer; only registered when `config.VLMModelID != ""`. Chat Completions can't carry images in tool-role messages, so VLM describes them into text. |
| Wrappers (not external calls) | `internal/models/vlm/concurrency_wrapper.go:27`, `internal/models/vlm/langfuse_wrapper.go:24,46`, `internal/models/vlm/llm_debug.go:19` | `inner.Predict(...)` passthrough | Decorator chain applied in `NewVLM`: `debugVLM → langfuse → concurrency-gate`. |

## 2. `vlm` package surface

- Interface: `internal/models/vlm/vlm.go:9-15` — `VLM { Predict(ctx, imgBytes [][]byte, prompt string) (string,error); GetModelName(); GetModelID() }`.
- `Config` struct: `internal/models/vlm/vlm.go:18-38` — `Source, BaseURL, ModelName, APIKey, ModelID, InterfaceType ("ollama"|"openai"), Provider, MaxConcurrency, Extra map[string]any, CustomHeaders, AppID, AppSecret`.
- `ConfigFromModel`: `internal/models/vlm/vlm.go:45-78` — maps `types.Model` → `Config`; `InterfaceType` defaults to `ollama` for `ModelSourceLocal`, else `openai`; propagates `ExtraConfig→Extra`, `CustomHeaders`, cloud `appID/appSecret`.
- `newVLM` dispatch: `internal/models/vlm/vlm.go:96-116` — `ollama`/local → `NewOllamaVLM`; else `providerName = Provider || DetectProvider(BaseURL)`; WeKnoraCloud → `NewWeKnoraCloudVLM` (`weknoracloud.go`); else `NewRemoteAPIVLM`.
- `NewRemoteAPIVLM`: `internal/models/vlm/remote_api.go:55-122` — SSRF-validates BaseURL (`transport.go:validateVLMBaseURL`); Azure branch (`ProviderAzureOpenAI`: `openai.DefaultAzureConfig` + `AzureModelMapperFunc` identity + `Extra["api_version"]`); non-Azure strips `/api/v1/chat/completions` or `/chat/completions` suffix via `modelutils.StripPathSuffix` (egress guard) then `openai.DefaultConfig`. HTTP client = SSRF-safe (`newVLMHTTPClient`, `transport.go:16-20`) + optional `WrapHTTPClientWithHeaders`. Temp default `0.1`, overridable via `Extra["temperature"]` string. Timeout `VLM_HTTP_TIMEOUT_SECONDS` else 180s; `MaxTokens 5000`.
- `DetectProvider` fallback: `internal/models/provider/provider.go:229+` — substring match on BaseURL (aliyun, zhipu, openrouter, litellm, azure `openai.azure.com`, `api.openai.com`, deepseek, gemini, volcengine, hunyuan, minimax, mimo, gpustack, modelscope, qiniu, moonshot, qianfan, longcat, lkeap, ...). Loopback stays generic (SSRF-blocked unless whitelisted).
- `GetVLMModel` (service): `internal/application/service/model.go:640-674` — `MustTenantIDFromContext` → `repo.GetByID` → `resolveWeKnoraCloudCredentials` → `vlm.NewVLM(vlm.ConfigFromModel(...), ollamaService)`.
- `resolveVLM` (image path): `internal/application/service/image_multimodal.go:517-551` — KB lookup → `ProcessOverrides` → `ResolveProcessConfig(...).VLMConfig` → `IsEnabled` gate → new-style (`ModelID` → `GetVLMModel`) or legacy (`NewVLMFromLegacyConfig`, `vlm.go:119-142`, inline BaseURL/APIKey/ModelName, ollama iff InterfaceType==ollama).
- `VLMConfig` struct: `internal/types/knowledgebase.go:563-585` — `Enabled, ModelID, DescriptionLanguage, CustomInstructions, ModelName, BaseURL, APIKey, InterfaceType`; `IsEnabled()` = `(Enabled && ModelID) || (ModelName && BaseURL)`.

## 3. Existing VLM tests + stub patterns

- `internal/models/vlm/remote_api_reasoning_test.go` — httptest pattern: `newVLMChatTestServer` (lines ~113-135) decodes generic `map[string]interface{}` body, records `lastRequest`, returns fixed `chat.completion` envelope; `testPNG` minimal PNG bytes. Covers reasoning shaping (gpt-5/o-series: `max_tokens→max_completion_tokens`, drop temperature), non-reasoning guard (gpt-4o keeps `max_tokens`+temp), truncated-completion error (`finish_reason=length` + empty content → error, keeps out of `no_extracted_content` bucket), unshaped-request rejection pins upstream go-openai behavior.
- SSRF whitelist handling: `internal/models/vlm/transport_security_test.go:9-13` `withVLMSSRFWhitelist(t, raw)` — `t.Setenv("SSRF_WHITELIST", raw)` + `ResetSSRFWhitelistForTest` with cleanup; reasoning tests pass `"127.0.0.1"` so httptest loopback is allowed; `TestRemoteAPIVLMRejectsInternalBaseURL` (line 17) passes `""` and asserts `169.254.169.254` is rejected at `NewRemoteAPIVLM`.
- `internal/models/vlm/config_from_model_test.go` — pure mapping tests (remote→openai default, local→ollama default, explicit InterfaceType wins, Extra/CustomHeaders/cloud creds propagated).
- Service stubs: `internal/application/service/temporary_document_test.go:17-62` — `fakeVLM` (mutex-guarded call counter, fixed response; race-safe for concurrent page OCR) and `promptAwareVLM` (routes on `strings.Contains(prompt, "description of the main content")` to count OCR vs caption calls) + `fakeVLMModelService` overriding `GetVLMModel` (line 70). Also `session_knowledge_qa_test.go:130`, `tenant_skill_install_test.go:3334` stub `GetVLMModel`.

## 4. OCR vs caption prompts + image byte arrival

- OCR prompts (`image_multimodal.go:19-44`): `vlmOCRPrompt` (generic doc OCR: ignore header/footer, md tables, LaTeX, reading order, output-only, `No text content` sentinel) vs `vlmOCRScannedPDFPrompt` (selected when `ImageSourceType=="scanned_pdf"`, adds layout/hierarchy preservation, ignores page numbers). Both extended via `AppendCustomPromptInstructions(prompt, CustomInstructions, "image_ocr")`. `temporary_document.go:513-515` reuses same constants.
- Caption prompt (`image_multimodal.go:53-60`): `fmt.Sprintf("Provide a brief and concise description of the main content of the image in %s.", language)` where language = `cfg.DescriptionLanguage` else `LanguageNameFromContext(ctx)`, plus `AppendCustomPromptInstructions(..., "image_description")`. Chat-upload path instead uses `buildImageAnalysisPrompt` (`image_upload.go:99-112`, Chinese, query-aware single call).
- Byte arrival: `readImageBytes` (`image_multimodal.go:586-623`) — `provider://`/`resource://` → `FileService.GetFile` + `io.ReadAll` (never HTTP; cf. issue #1282), `ImageLocalPath` → `os.ReadFile`, else `http(s)` → `secutils.DownloadBytes` (SSRF-safe downloader). `temporary_document.go:573-589` `collectImageBytes` caps inline `ImageRef.ImageData` at `limit`; size guard at upload (`temporary_document.go:162-169`, `GetMaxFileSizeMB`). `RemoteAPIVLM.Predict` (`remote_api.go:125-192`) base64-encodes each image as `data:<mime>;base64,...` (`detectImageMIME` via `http.DetectContentType`, fallback `image/png`), `Detail: auto`, logs `numImages/totalImageSize` (no byte cap in VLM layer).

## 5. Chat-path Responses reuse candidates (for Responses-based VLM)

- `internal/models/chat/responses.go:buildResponsesInputValue` (~line 219) — structured input builder already handles `image_url` MultiContent parts → `input_image` items; VLM Predict's current `ChatMessagePart{ImageURL: dataURI}` loop maps 1:1 onto this. Reuse/parallel the data-URI construction.
- `internal/models/chat/responses.go:parseResponsesBody` (~line 263) — envelope decode, `error.message` → error, join `output_text`, `status` → FinishReason (`completed`/`""`→`stop`, else status). Needed so truncated/incomplete Responses (`status:incomplete`) don't collapse into "no content" — mirrors the `finish_reason=length` guard in `remote_api.go:158-172`.
- `internal/models/provider/responses.go:NormalizeBaseURL` (line 56) — strips `/responses` (plus chat-completions suffixes) for Responses provider; other providers trim only. VLM Responses constructor will need the same ingress normalization.
- `internal/models/utils/url.go:AppendPathOnce` (line 9) / `StripPathSuffix` (line 21) — single home for endpoint guards; chat path uses `responsesEndpoint = AppendPathOnce(base, "/responses")` (`responses.go:75`) and `newResponsesHTTPRequest` bundles marshal + SSRF check + auth + custom headers (`responses.go:89-110`) — template for the VLM Responses request builder.
- Reasoning/effort: chat `responsesRequest{MaxOutputTokens, Reasoning{Effort}}` + `resolveResponsesEffort` (`responses.go:26-55`) parallels VLM `shapeReasoningVLMRequest` (`remote_api.go:194-213`) + `IsOpenAIReasoningOrGPT5Model` (`provider/openai.go:70`); decide effort mapping (`reasoning_effort` ExtraConfig) when porting.

## Start Here (for implementer of #22 follow-up)

1. `internal/models/vlm/remote_api.go:125` (`Predict`) — the method to add a Responses branch to.
2. `internal/models/chat/responses.go:89-110,219,263` — request builder, input builder, body parser to mirror.
3. `internal/models/vlm/remote_api_reasoning_test.go:113+` — httptest + SSRF-whitelist pattern to copy for Responses VLM tests.

_Source: scout recon for issue #22, part of map #21._
