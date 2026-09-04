# WeKnora /chat/completions Hardcoding Map (issue #11, part of #9)

Read-only recon. No code modified.

## 1. Raw-HTTP fallback appends `/chat/completions` unconditionally
- `internal/models/chat/remote_api.go:212` — `chatWithRawHTTP`: `if endpoint == "" { endpoint = c.baseURL + "/chat/completions" }` (non-stream).
- `internal/models/chat/remote_api.go:343` — `chatStreamWithRawHTTP`: same append (stream). Reached when `buildOutbound` returns `useRawHTTP=true` (thinking custom body, `ForceRawHTTP`, or non-empty adapter endpoint).
- Risk: any stored `base_url` already ending in `/chat/completions` double-appends (404). No `HasSuffix` guard.

## 2. SDK path inherits the same assumption via go-openai
- `internal/models/chat/remote_api.go:62-76` (`NewRemoteAPIChat`): sets `config.BaseURL = chatConfig.BaseURL`, stores `TrimRight(config.BaseURL, "/")`. Standard-path requests go through `client.CreateChatCompletion` / `CreateChatCompletionStream` (~lines 178, 281); go-openai appends `/chat/completions` internally. A full-path base_url breaks the SDK path too.
- Azure variant (`remote_api.go:55-61`) uses `DefaultAzureConfig` — different URL scheme, unaffected.

## 3. Adapter endpoint contract documents the assumption
- `internal/models/chat/provider.go:44-46` — `Endpoint()` doc: empty string means the standard `"<baseURL>/chat/completions"`.
- `internal/models/chat/provider.go:88` — `weKnoraCloudProvider.Endpoint`: `TrimRight(baseURL)+"/api/v1/chat/completions"` (intentional non-standard endpoint; `ForceRawHTTP`, line 91).
- `internal/models/vlm/weknoracloud.go:18,107` — VLM parallel: `weKnoraCloudVLMPath = "/api/v1/chat/completions"`, `v.baseURL+weKnoraCloudVLMPath`. Same double-append risk.

## 4. Connection-test path rides the same code
- `internal/handler/initialization.go:1754` (`CheckRemoteModel`) -> `checkChatModelConnection` at `:1913-1949`: `buildTestModel` + `chat.NewChat(chat.ConfigFromModel(...))`, sends `Chat(ctx, [{user,test}], {MaxTokens:1, Thinking:false})`. `/responses`-only backends fail here identically; the `400 = success` carve-out (`:1932`) only masks param mismatches, not wrong-endpoint 404s.
- Test forces `Thinking=false`, so Qwen `enable_thinking` / generic `chat_template_kwargs` raw-HTTP branch is exercised in the check.

## 5. Streaming has no independent endpoint logic
- Stream and non-stream share `buildOutbound` (`remote_api.go:125-150`); only `isStream` differs (passed to `adapter.Endpoint` and thinking strategies e.g. Qwen `disableOnNonStream`). Both stream entries (SDK `:281`, raw `:~344`) resolve URLs identically to non-stream.

## 6. `chat_template_kwargs.enable_thinking` payload (generic/vLLM/nvidia/litellm)
- `internal/models/chat/thinking.go:110-125` (`chatTemplateKwargs.Apply`): sets `req.ChatTemplateKwargs = {"enable_thinking": ...}`, returns `(req, true)` → raw HTTP whenever thinking is set.
- Default thinking for `genericProvider`, `nvidiaProvider`, `liteLLMProvider` (`provider.go:138-150`); also the fallback for unknown `thinking_control` values in `parseThinkingOverride` (`thinking.go:130-145`). A `/responses` backend needs a new strategy + mapping here plus a frontend option.
- Qwen contrast: `enableThinking` (`thinking.go:61-89`, `QwenChatCompletionRequest.EnableThinking` lines 21-25) forces raw HTTP always (`alwaysSend`, `provider.go:96-101`).

## 7. Frontend provider dropdown + i18n labels
- `frontend/src/components/ModelEditorDialog.vue:505-645` `fallbackProviderOptions` (used when providers API unreachable): hardcodes `defaultUrls.chat` per provider; `providerOptions` computed at `:665-683` prefers API data, falls back to these.
- Thinking selector: `ModelEditorDialog.vue:365-385, 700-730` + `frontend/src/utils/thinkingControl.ts:19-51` (`none | chat_template_kwargs | enable_thinking | thinking_type`); labels in `frontend/src/i18n/locales/{en-US.ts:4255-4259, zh-CN.ts:2525-2529, ru-RU.ts:2523-2527, ko-KR.ts:2523-2527}`.
- Live metadata from `internal/models/provider/*.go` `Info().DefaultURLs`; generic has empty `DefaultURLs` (`generic.go:19`) so users paste arbitrary base URLs there — highest double-append exposure.

## 8. `config/builtin_models.yaml` wiring
- Only `config/builtin_models.yaml.example` exists in-repo; loader is `internal/types/builtin_models_config.go:1-60+` (`BuiltinModelEntry`, `${ENV}` interpolation, upsert with `managed_by=yaml`). Example uses prefix-form `base_url: https://api.openai.com/v1` — correct convention, but nothing validates suffix; a full-path value flows verbatim via `ModelParameters.BaseURL` → `chat.go:144 ConfigFromModel` → append in sections 1-2. See also `docs/BUILTIN_MODELS.md`.

## 9. Stored `deepseek-v4-flash` double-append flag
- No `deepseek-v4-flash` base_url literal in repo (grep hits only `internal/models/chat/provider_test.go:70` test model name and description strings). The flagged stored model is DB/runtime data, not a checked-in constant — verify via DB (`models` table `parameters->>'base_url'`).
- Mechanism if confirmed: `NewRemoteAPIChat` keeps the full-path baseURL; raw-HTTP append (section 1) or SDK append (section 2) yields `.../chat/completions/chat/completions`. Fix direction: normalize/strip known suffixes at ingress or guard appends with `strings.HasSuffix`, shared by chat + VLM paths.

## Start here (fix agent)
1. `internal/models/chat/remote_api.go` (~125-150, 200-215, 335-350) — central URL resolution.
2. `internal/models/chat/provider.go` (40-95) — endpoint contract + WeKnoraCloud exception.
3. `internal/models/chat/thinking.go` (full, ~170 lines) — wire formats for `/responses` mapping.
4. `internal/handler/initialization.go:1913-1949` — connection-test must match fix.
5. `internal/models/vlm/weknoracloud.go:18,107` — same normalization.

## Open questions
- Strip only trailing `/chat/completions`, or also `/api/v1/chat/completions` (WeKnoraCloud)? Must be allowlist-scoped (Azure/proxies need suffixes preserved).
- Normalize at ingress (model save / builtin loader / `ConfigFromModel` `chat.go:144`) vs egress (append sites)? Ingress fixes future rows; egress guard protects existing rows like the flagged deepseek-v4-flash. Likely both.
