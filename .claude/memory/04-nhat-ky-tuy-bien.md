# 04 — Nhật ký & Inventory tùy biến (vs upstream)

> **Mục đích:** theo dõi MỌI khác biệt so với `Tencent/WeKnora` để quản lý `git merge upstream` và bàn giao.
> **Quy tắc cho AI agent:** khi sửa **file dùng chung của upstream** hoặc **thêm migration/schema**, PHẢI thêm dòng vào bảng phù hợp trong cùng PR.

## Thông tin fork
- **Upstream:** `https://github.com/Tencent/WeKnora` · **Origin:** `github.com/covuaduongsinh/WeKnora_chess`
- **Nền:** v0.6.2 · **Nhánh:** `main` · **Production:** `weknora.covuaduongsinh.com`
- **Điểm rẽ nhánh (merge-base) lúc lập inventory:** `5d0d317a` (2026-06-22). Upstream tip khi đó: `7d8a80ae` (2026-06-28).
- **Quy mô tùy biến (diff `upstream/main...HEAD`):** ~75 file lớp cờ (mới) · **76 file dùng chung bị SỬA** · 1 file bị XÓA · 39 file mới non-chess (deploy/lite/i18n).
- **Cập nhật lại inventory:**
  ```bash
  git fetch upstream main --no-tags
  git diff --name-status -M upstream/main...HEAD     # phần fork đã đổi so với điểm chung
  ```

---

## A. Lớp cờ — code RIÊNG (rủi ro conflict THẤP, ~75 file)
| Vùng | Đường dẫn |
|---|---|
| Engine | `internal/chess/` (board, engine, uci_engine, http_engine, *_test) |
| Agent tools | `internal/agent/tools/chess_*.go` (6 tool + common + demo_test) |
| Repository | `internal/application/repository/chess_*`, `wiki_chess_ref.go` |
| Service | `internal/application/service/chess_*` |
| Handler | `internal/handler/chess_*` |
| Types | `internal/types/(interfaces/)chess_*`, `wiki_chess_ref.go` |
| Frontend | `frontend/src/views/chess/**`, `views/chat/components/tool-results/ChessBoardDisplay.vue`, `api/chess/`, `stores/chessWikiDraft.ts`, `utils/chessBlocks.ts`, `utils/chessRef.ts` |
| Docker engine | `docker-compose.chess.yml`, `docker/Dockerfile.chess-engine`, `docker/chess-engine/uci_http_bridge.py` |
| Deploy/Docs | `scripts/deploy/weknora-chess.service`, `scripts/seed_chess_wikilink_demo.*`, `docs/chess-wikilink-demo.md` |

## B. Migrations cờ (NỐI TIẾP upstream) — `000062`–`000070`
courses · games_puzzles · slugs · wiki_chess_refs · course_slug · refs_source_type · slug_aliases · kb_index · **`000070` chess_positions** (thực thể mới — Ngân hàng thế cờ, xem mục E).
> Khi merge: nếu upstream thêm migration trùng dải số → đổi số migration cờ cho cao hơn.

---

## C. FILE DÙNG CHUNG ĐÃ SỬA (rủi ro conflict CAO) — 76 file
*(số "N×" = số lần xuất hiện chữ "chess" trong file, đo lúc lập inventory — càng cao càng là điểm móc lõi.)*

### C1 — Móc nối backend (BẮT BUỘC để ý khi merge)
| File | Vai trò móc nối cờ | Mức |
|---|---|---|
| `internal/application/service/agent_service.go` | Gắn agent/tool cờ vào luồng agent. **WS1 (nối Puzzle Bank):** +field `chessLibraryService` + param `NewAgentService` + truyền vào `NewChessGeneratePuzzleTool(s.chessLibraryService)` để tool ra bài tập từ DB. | 32× |
| `internal/config/config.go` | Đọc `WEKNORA_CHESS_*`, `CHESS_KB_INDEX` | 23× |
| `internal/agent/tools/definitions.go` | Đăng ký 6 tool `chess_*` vào registry | 21× |
| `internal/router/router.go` | Mount route `/api/v1/chess/...`. **WS2:** +nhóm `/chess/library` với `POST /reindex` (backfill KB cờ). **WS-RAG:** +`GET /chess/library/index-status` (chẩn đoán RAG, Contributor). | 17× |
| `internal/container/container.go` | Wiring DI service/repo cờ | 11× |
| `internal/types/custom_agent.go` | Field phục vụ agent cờ | 3× |
| `config/builtin_agents.yaml` | Agent `builtin-chess-coach` | (block cuối) |
| `internal/agent/act.go` | **0× chess** — sửa vì lý do khác (rà lại khi merge) | 0× |

### C2 — Tích hợp Wiki ↔ wikilink cờ
| File | Vai trò | Mức |
|---|---|---|
| `internal/application/service/wiki_page.go` | Trang wiki nhận diện/giải link cờ | 39× |
| `internal/types/wiki_page.go` | Kiểu dữ liệu wiki + ref cờ | 15× |
| `internal/application/service/wiki_ingest.go` | Expand wikilink khi embed | 1× |
| `internal/application/service/wiki_lint.go` | Lint wiki có ref cờ | 1× |
| `frontend/src/views/knowledge/wiki/WikiBrowser.vue` | Hiển thị node cờ trong graph | — |

### C3 — Móc nối frontend
| File | Vai trò | Mức |
|---|---|---|
| `frontend/src/types/tool-results.ts` | Kiểu kết quả tool cờ | 8× |
| `frontend/src/views/chat/components/ToolResultRenderer.vue` | Render bàn cờ từ kết quả tool | 4× |
| `frontend/src/router/index.ts` | Route khu "Quản lý cờ vua" | 3× |
| `frontend/src/components/menu.vue` | Mục menu cờ | 2× |
| `frontend/src/stores/menu.ts` | State menu | 1× |
| `frontend/src/utils/agent-tool-icons.ts` | Icon cho tool cờ | 1× |

### C4 — i18n & prompt templates (Việt hóa + bỏ tiếng Trung)
- **Bỏ** `frontend/src/i18n/locales/zh-CN.ts` (D) · **Thêm** `locales/vi-VN.ts` (A).
- Sửa: `i18n/index.ts`, `i18n/embed.ts`, `locales/en-US.ts`, `locales/ko-KR.ts`, `locales/ru-RU.ts`.
- Prompt: `config/prompt_templates/` → `agent_system_prompt.yaml`, `context_template.yaml`, `fallback.yaml`, `intent_prompts.yaml`, `rewrite.yaml`, `system_prompt.yaml`.
> Đây là vùng dễ conflict text khi upstream đổi prompt/i18n — thường tự giải quyết được, nhưng kiểm tra kỹ vi-VN không bị mất.

### C5 — Hạ tầng / build / deploy (khác biệt lớn ngoài lớp cờ)
- **Build/dep:** `Makefile`, `docker/Dockerfile.app`, `go.mod`, `go.sum`, `frontend/package.json`, `frontend/package-lock.json`, `frontend/index.html`, `frontend/env.d.ts`, `.gitattributes`, `.env.example`.
- **CI/CD & deploy riêng (mới, A):** `.github/workflows/cicd-deploy.yml`, `docker-compose.caddy.yml`, `docker-compose.ghcr.yml`, `docker-compose.override.yml`, `docker/caddy/Caddyfile`, `scripts/deploy/{backup,restore,dc,pull-deploy,redeploy,server-bootstrap}.sh`, `scripts/deploy/hetzner-cloud-init.yaml`, `scripts/dev/{local-deploy,local-status}.sh`, `scripts/dev/githooks/pre-push`, `docs/deploy/{backup-restore,cicd,dev-workflow,hetzner,https}.md`. Sửa: `.github/workflows/docker-image.yml`.
- **mcp-server:** sửa `main.py`, `run_server.py`, `run.py`, `setup.py`, `test_imports.py`, `test_module.py` + thêm `uv.lock`, `*.egg-info/*`.

### C6 — Bản chạy SQLite ("lite") — mới
- Sửa `migrations/sqlite/000000_init.up.sql`; **thêm** `migrations/sqlite/000001..000003*` (wiki_and_indexing, lite_missing_tables, messages_attachments), `build_sqliteshim/*.h`, `frontend/src/utils/fileTransfer.ts`.
- Liên quan: `.env.lite`, `weknora-lite.log` ở root.
> Fork có nhánh chạy **SQLite** thay PostgreSQL (bản nhẹ) — lưu ý khi merge migration upstream (chỉ có bản postgres).

### C7 — File dùng chung khác bị sửa (không chứa "chess")
`internal/im/{cmd_clear,cmd_help,cmd_info,cmd_search,cmd_stop,service}.go`, `internal/im/feishu/adapter.go`, `internal/errors/errors.go`, `internal/middleware/language.go`, `internal/types/{context_helpers,context_helpers_test,placeholder}.go`, `frontend/src/App.vue`, `frontend/src/views/auth/Login.vue`, `frontend/src/api/chat/streame.ts`, `frontend/src/api/embed/index.ts`, `frontend/src/components/{AgentEmbedChannelPanel,manual-knowledge-editor,MyInvitationsDialog}.vue`, `frontend/src/views/chat/components/{AgentStreamDisplay,botmsg}.vue`, `frontend/src/views/chat/components/tool-results/WebSearchResults.vue`, `frontend/src/views/settings/{GeneralSettings,TenantInfo,TenantMembers,UserProfile}.vue`, `frontend/src/views/system/SystemSettings.vue`, `frontend/src/utils/request.ts`, `data/weknora.db.bak-131441`.
> Phần lớn là Việt hóa/tinh chỉnh UI/IM. Rà từng cái khi conflict; ưu tiên giữ thay đổi tối thiểu.

---

## D. Quyết định kiến trúc (ADR rút gọn)
- **Engine:** Arasan (MIT) sidecar **HTTP** (UCI→HTTP bridge), gọi qua `WEKNORA_CHESS_*`.
- **Agent HLV:** `kb_selection_mode: none` — mặc định KHÔNG RAG, chỉ 7 tool cờ + engine (thêm `chess_lookup_position` từ đợt Ngân hàng thế cờ, mục E). *(Bật `CHESS_KB_INDEX` + thêm `knowledge_search` để trích dẫn lý thuyết/sách.)*
- **RAG cờ:** gate `CHESS_KB_INDEX` (mặc định TẮT); import PGN hàng loạt KHÔNG trigger index.
- **Wikilink:** slug bất biến; resolve `exact → alias → fuzzy` (bigram-Jaccard ≥ 0.8); bảng `chess_slug_aliases`.
- **i18n:** bỏ `zh-CN`, chuẩn hóa **vi-VN** làm ngôn ngữ chính.
- **Triển khai:** thêm bản **SQLite "lite"** + bộ **deploy Caddy/Hetzner/CI-CD** riêng (ngoài upstream).

## E. Backlog tùy biến

### Tiến độ thực thi plan "nối dây + đổ xăng" (2026-06-29)
- [x] **WS1** — Nối Puzzle Bank → `chess_generate_puzzle` (tool đọc DB qua `PuzzleSource`/`RandomPuzzle`, fallback embedded). Sửa `agent_service.go` (C1). Test: `chess_generate_puzzle_test.go`.
- [x] **WS3** — Mở rộng khai cuộc: nhúng `data/eco.tsv` (3733 khai cuộc, lichess CC0) qua `chess_openings_data.go`; `openingIndex` gộp dataset + overlay Việt hoá; `chess_lookup_opening.go` dùng index. Test: `chess_lookup_opening_test.go`.
- [x] **WS2 (chuẩn bị, CHƯA bật)** — `ReindexAll` (service+interface chess) + route `POST /chess/library/reindex` (router.go C1) + runbook `docs/chess-rag-enable.md`. Bật runtime do Thầy theo runbook.
- [x] **WS4b** — thông báo lỗi engine thân thiện (`friendlyEngineError` trong `chess_common.go`, áp cho analyze/best_move/explain_move); `httpEngine.Health()` probe + cảnh báo sớm lúc init trong `getChessEngine` (agent_service.go C1). Test: `chess_engine_error_test.go`. (Resolve fuzzy đã có test sẵn `chess_resolve_test.go`.) *Còn nợ:* endpoint `/chess/engine/health` (cần refactor engine thành service DI) — để sau.
- [x] **WS4a** — áp thương hiệu Dương Sinh: file đè `frontend/src/assets/theme/duongsinh-brand.css` (thang `--td-brand-color-*` → navy #2B3990, light+dark) import sau theme.css ở `main.ts`/`embed-main.ts` (C3 frontend); `index.html` đổi title + favicon SVG + meta; logo `frontend/public/duongsinh-{symbol,logo}.svg` (từ `@ds/brand`). Build frontend OK. *Lưu ý:* brand thật là navy + xanh, KHÔNG cam/gold (đã đính chính `01-du-an-duongsinh.md`).

### Đợt củng cố sau audit (2026-06-29) — nhánh `feat/chess-audit-hardening`
Rà soát toàn dự án (3 agent: backend/frontend/hạ tầng) → triển khai 4 nhóm A–D.
- [x] **WS-A (vận hành & an toàn):**
  - Thêm `migrations/versioned/000068_*.down.sql` + `000069_*.down.sql` (trước đó thiếu cặp `.down` → không rollback được). Mục B nhật ký.
  - `docker-compose.chess.yml` (file cờ riêng): thêm `healthcheck` cho `chess-engine` (probe `/health`) + `app.depends_on` đổi sang `condition: service_healthy`.
  - `.gitignore`: thêm `data/*.bak*`; `git rm --cached data/weknora.db.bak-131441` (gỡ khỏi track, giữ file local).
  - `.github/workflows/cicd-deploy.yml` (file CI riêng, C5): thêm job `test` (go vet+test package cờ, frontend type-check) làm gate trước `deploy`.
- [x] **WS-B (củng cố backend):**
  - Test 4 tool: thêm `chess_analyze_position_test.go`, `chess_best_move_test.go`, `chess_explain_move_test.go`, `chess_evaluate_game_test.go` (tái dùng `stubEngine`/`errEngine`, thêm `emptyMoveEngine`).
  - Gom trùng lặp: `validateFENArg` trong `chess_common.go` (áp analyze/best_move/explain_move); gộp `chessSideToMove`→`fenSide` (xóa bản trùng ở demo test); lookup dùng `fenSide`.
  - Config hóa: thêm `EvaluateDepth`/`EvaluateMaxPlies`/`LookupOpeningLimit` vào `ChessConfig` (`config.go` C1) + env `CHESS_EVALUATE_DEPTH`/`CHESS_EVALUATE_MAX_PLIES`/`CHESS_LOOKUP_OPENING_LIMIT` (`.env.example`); constructor tool nhận tham số; `agent_service.go` (C1) thêm getter + truyền vào `NewChessEvaluateGameTool`/`NewChessLookupOpeningTool`.
  - Endpoint `GET /chess/engine/health`: file MỚI `internal/application/service/chess_engine.go` + `internal/handler/chess_engine.go` + `internal/types/interfaces/chess_engine.go`; wiring `container.go` (C1, +2 Provide) + route `router.go` (C1, `RegisterChessEngineRoutes`, Viewer). **Quyết định:** làm service ENGINE ĐỘC LẬP (không sửa `NewAgentService`) để giữ file xung đột cao nhất tối thiểu — engine http là wrapper rẻ nên chấp nhận init lười riêng. Trả `{enabled, healthy, detail}`.
- [x] **WS-C (frontend/brand):**
  - Font Roboto self-host (Apache-2.0): 9 file woff2 (latin/latin-ext/vietnamese × 400/500/700) ở `frontend/public/fonts/` + `@font-face` trong `duongsinh-brand.css`. Build OK, dist copy đủ.
  - Họa tiết ô cờ (checkerboard rất nhạt) cho `.chess-manage` (scoped, không đụng global).
  - Validate FEN khi nhập: `isValidFEN()` trong `utils/chessBlocks.ts` (validator cấu trúc, không import chess.js để khỏi phình bundle chat); áp ở `PuzzleBank.vue` + `ChessCourses.vue` (FEN bài học, chỉ khi nhập).
  - Type-safe: `PuzzleDialogState`/`CourseDialogState`/`LessonDialogState` thay `reactive<any>`; fix viewer PuzzleBank cập nhật sau khi sửa (revealKey++).
  - a11y: `menu.vue` (C3 shared) thêm `alt="Cờ vua Dương Sinh"` cho logo.
  - **CI đính chính:** `npm run type-check` (vue-tsc) ĐANG đỏ do lỗi type tồn đọng ở phần upstream (KnowledgeBase/ApiInfo/GraphSettings…) → ĐÃ BỎ khỏi gate `cicd-deploy.yml` (giữ gate Go; frontend vẫn dựng qua `vite build` ở build-ui). Bật lại type-check khi dọn sạch type upstream.
  - **HOÃN C3** (tách `ChessCourses.vue` 637 dòng): refactor thuần nội bộ, không thêm giá trị; picker gắn chặt template-ref `<textarea>` nên tách "mù" (không chạy được app để test runtime tại đây) dễ hồi quy → để lại backlog, làm khi chạy app kiểm thử được.
- [x] **WS-D (tính năng nợ):**
  - **D1 — RAG cờ:** runbook `docs/chess-rag-enable.md` đã khớp code; thêm mục **3b Production** turn-key (backup DB trước → set `CHESS_KB_INDEX=true` → redeploy → reindex prod URL → sửa agent YAML → nghiệm thu). KHÔNG commit đổi `builtin_agents.yaml` (toggle vận hành; flip sớm sẽ đổi hành vi HLV + `kb_selection_mode:all` kéo mọi KB trước khi KB cờ sẵn sàng) — Thầy bật theo runbook. Bật production là thao tác trên server (ngoài tầm tác động của agent ở đây).
  - **D2 — đổi tên slug:** thêm repo `UpdateGameSlug`/`UpdatePuzzleSlug` (tách riêng vì `UpdateGame`/`UpdatePuzzle` cố tình KHÔNG đụng cột slug); service `RenameGameSlug`/`RenamePuzzleSlug` (chuẩn hóa qua `slugifyChess` + `ensureUniqueChessSlug` + ghi `chess_slug_aliases` old→new); handler + route `PUT /chess/{games,puzzles}/:id/slug` (router.go C1, Contributor); frontend api `renameGameSlug`/`renamePuzzleSlug` + nút "Đổi slug" (prompt) ở `GameLibrary.vue`/`PuzzleBank.vue`. Bảng `chess_slug_aliases` (000068) nay được dùng → resolve `exact→alias→fuzzy` có dữ liệu. *Còn lại:* rename cho course/lesson (service khác) — backlog.

### Đợt sửa RAG sau khi bật production (2026-06-30) — nhánh `feat/chess-audit-hardening`
RAG production bật xong nhưng agent "tìm thấy tên nhưng không ra nội dung". Rà 3 agent + đọc code → **nguyên nhân gốc:** reindex báo `success` GIẢ (đếm số ván của tenant, không phải số đã embed); embedding chạy NỀN; bị SKIP im lặng nếu KB cờ không có embedding model → chunk lưu DB nhưng không lên vector store → `knowledge_search` rỗng. Triển khai "hoàn thiện sâu":
- **Endpoint chẩn đoán** `GET /chess/library/index-status` (Contributor): file MỚI logic trong `chess_knowledge_indexer.go` (`IndexStatus`), lộ qua `chessLibraryService.IndexStatus` + handler `chess_library.go` + route `router.go` (C1). Trả `{enabled, kb_exists, embedding_configured, total, completed, pending, failed, sample_error}`. Struct `ChessIndexStatus`/`ChessReindexResult` đặt ở `internal/types/chess_kb_index.go` (tránh import cycle interfaces→service).
- **Reindex trung thực + fail-loud:** `IndexGame/IndexPuzzle/IndexLesson/upsert` (indexer) nay TRẢ `error` (caller CRUD vẫn best-effort qua `_ =`); `ReindexAll` đổi chữ ký trả `*types.ChessReindexResult`, **chặn trước** khi KB cờ thiếu embedding model (lỗi rõ thay vì success giả), đếm thật (total/enqueued/failed/errors). Handler giữ `games_indexed`/`puzzles_indexed` để tương thích ngược + thêm `note` nhắc kiểm tra index-status. Đổi interface `ChessLibraryService` (file cờ riêng).
- **Agent prompt** (`builtin_agents.yaml`, C1): khi `knowledge_search` rỗng → nói "kho tri thức chưa có tài liệu khớp" + trả lời bằng kiến thức cờ + công cụ cờ; **CẤM bịa tên file/đường dẫn** (gemini-2.5-flash từng confabulate "Opera Game.md"). Threshold đã hạ trước đó (vector 0.3/rerank 0.1) — giữ.
- **Runbook** `docs/chess-rag-enable.md`: thêm mục **4b** bảng chẩn đoán theo `index-status` + SQL fallback.
- *Đa số diff trong file `*chess*` (an toàn merge); file dùng chung: `router.go` (1 route), `builtin_agents.yaml` (prompt).*

**Bổ sung sau khi soi LOG production (lỗi thật là CONFIG, không phải vector store):** Log hiện `ERROR tool not found: knowledge_search` + `knowledge_search tool not enabled` + `RetrieveKBOnlyWhenMentioned=true` (YAML set false). → (1) Effective config của chess-coach KHÁC YAML (builtin agent bị chỉnh qua UI → lưu DB → DB override YAML, xem `custom_agent.go:185-208`); bản DB thiếu knowledge_search nên LLM gọi (vì system_prompt nhắc tên) → "tool not found". (2) `session_agent_qa.go:112-116` chặn CỨNG: có knowledge_search mà thiếu `rerank_model_id` → lỗi; tenant Dương Sinh KHÔNG có rerank model (chỉ chat+embedding gemini). **Sửa (file dùng chung `internal/application/service/session_agent_qa.go` — MỚI vào inventory C7):** nới ràng buộc rerank thành TÙY CHỌN — thiếu rerank model thì cảnh báo + đi tiếp, để `knowledge_search` tự dùng LLM-based rerank fallback bằng chat model (fallback đã có sẵn `knowledge_search.go:287,301`). *Còn lại (vận hành):* xóa/ghi đè DB override của chess-coach để allowed_tools có knowledge_search (sửa qua UI hoặc xóa override).

**Bổ sung sau khi index-status chạy (vẫn rỗng dù 7/7 completed):** Đào tiếp tới `session_knowledge_qa.go` (KBSelectionMode=all) + `tools/capabilities.go`. **NGUYÊN NHÂN GỐC THẬT:** `knowledge_search` yêu cầu KB có capability `vector|keyword` (`KBSatisfiesAgentRequirements`); capability lấy từ `KnowledgeBase.IndexingStrategy.{Vector,Keyword}Enabled`. KB "Tri thức cờ vua" production có vector+keyword=FALSE → (a) bị loại khỏi tập search của agent VÀ khỏi @picker frontend; (b) `processChunks` skip embedding (`NeedsEmbeddingModel`=false) nên không có vector — `completed` chỉ là parse xong, KHÔNG đảm bảo đã embed. `ensureChessKB` tạo KB KHÔNG set `IndexingStrategy` (dựa `EnsureDefaults`); KB cũ trên prod dính false. **Sửa:** (1) `ensureChessKB` set TƯỜNG MINH `IndexingStrategy: types.DefaultIndexingStrategy()` khi tạo + cảnh báo nếu KB cũ tắt index; (2) index-status thêm `vector_enabled/keyword_enabled/searchable` + `enabled_docs/disabled_docs` để xác nhận; (3) runbook 4b: nhánh `searchable:false` → XÓA KB cờ + reindex (code mới tạo lại đúng). Vận hành: xóa "Tri thức cờ vua" trên prod → reindex → KB mới có vector+keyword bật → embedding chạy thật.

### Ngân hàng thế cờ FEN — position bank (2026-08-10) — nhánh `feat/chess-position-bank`
Thêm thực thể cờ THỨ 5 `position` (bên cạnh game/puzzle/lesson/course), wikilink `[[position/<slug>]]`. **Ranh giới:** puzzle = bài TẬP có lời giải, để LUYỆN (không đổi); position = thế cờ THAM CHIẾU để DẠY/trích dẫn/phân tích (tàn cuộc lý thuyết, tabiya khai cuộc, mô-típ chiến thuật/cấu trúc tốt) — **CỐ Ý cho phép FEN không có quân Vua** (thế cờ giản lược dạy trẻ mới học).

**Kiểm chứng ràng buộc "không Vua" trước khi code (không cần thoả hiệp):** `notnil/chess` (`decodeFEN`) không kiểm tra sự tồn tại của Vua, chỉ đòi đủ 6 trường + cấu trúc — nên `chess.ValidateFEN`/`isValidFEN` (frontend) dùng lại được nguyên vẹn. Ba nơi PHẢI tránh: (1) chess.js frontend bắt buộc đúng 2 Vua → `ChessBoardDisplay`/`ChessRefEmbed` dữ liệu position **không bao giờ set `pgn`**, chỉ `fen`; (2) engine Arasan không phân tích được → không gọi engine cho position; (3) `chess.FENAfterMove`/`UCIToSAN`/`gameFromFEN` dựng `notnil.Game` → không dùng cho position.

**Quyết định kiến trúc:** position là "ngăn" thứ ba của `chessLibraryService`/`chessLibraryRepository`/`ChessLibraryHandler` (file mới `chess_library_position.go`, `chess_position.go` repo/handler — method trên CÙNG struct), **không phải service riêng** → `container.go` **0 dòng thay đổi**; `router.go` chỉ thêm 1 nhóm route trong `RegisterChessLibraryRoutes` đã có.

- **Backend mới (file riêng, an toàn merge):** migration `000070_chess_positions` (mục B); `internal/chess/fen.go` (`NormalizeFEN` bù trường thiếu, `FENKey`, `HasBothKings`, `SideToMove` export) + test; `internal/types/chess_position.go`; `internal/application/repository/chess_position.go`; `internal/application/service/chess_library_position.go`; `internal/handler/chess_position.go`; `internal/agent/tools/chess_lookup_position.go` (tool thứ 7, mẫu `PuzzleSource`, có bộ thế cờ mẫu nhúng sẵn gồm 1 thế KHÔNG Vua) + test; `frontend/src/views/chess/PositionBank.vue` + `components/ChessPositionEditor.vue` (trình soạn thế cờ bằng chuột — cm-chessboard không validate luật gì, xác nhận qua đọc source; **không dùng chess.js**) + `frontend/src/utils/chessPositionOptions.ts`.
- **File dùng chung bị chạm (đã có sẵn trong inventory C1/C2, chỉ thêm dòng):** `internal/types/wiki_chess_ref.go` (+`ChessRefTypePosition`/`ChessRefSourcePosition`), `internal/application/service/wiki_page.go` (+1 dòng `chessRefPrefixes` — thiếu dòng này thì `[[position/x]]` bị coi là link wiki thường, lỗi CÂM), `internal/types/wiki_page.go` (+`WikiNodeTypeChessPosition` + nhánh `ChessRefTypeToNodeType`), `internal/handler/chess_ref.go` (+nhánh `SearchRefs`), `internal/application/repository/wiki_chess_ref.go` (+`ReplaceForPosition`/`DeleteForPosition` mẫu `ReplaceForLesson` — xóa theo `tenant_id+page_slug`, KHÔNG theo `kb_id`; +khối query thứ 3 trong `ListBacklinks`), `internal/application/service/agent_service.go` (+1 case đăng ký tool), `internal/agent/tools/definitions.go` (+hằng tên/nhãn/`ChessToolNames`), `config/builtin_agents.yaml` (+tool + 1 dòng prompt), `internal/application/service/chess_knowledge_indexer.go`/`chess_knowledge_text.go` (+`IndexPosition`/`buildPositionKnowledgeText`), `internal/types/chess_kb_index.go` (+`PositionsTotal`).
- **Frontend dùng chung bị chạm:** `utils/chessRef.ts` + `utils/chessBlocks.ts` (union `ChessRefType`/`CHESS_REF_TYPES` +`position`), `api/chess/index.ts` (+nhóm hàm `*Position*`), `components/{ChessWikiLinkSuggest,ChessRefMissing,ChessRefEmbed}.vue` (+nhãn/icon/màu), **`components/ChessBacklinks.vue`** (sửa bug có sẵn: nhị phân `lesson`/*else* khiến backlink `position` từng rơi nhầm route KB rỗng — nay 3 nhánh rõ ràng), `views/knowledge/wiki/WikiBrowser.vue` (+màu/nhãn/`nodeColorMap`/`isChessNodeType` — thiếu dòng cuối thì node đồ thị không click mở được), `views/chat/components/tool-results/ChessBoardDisplay.vue` (+`defineExpose({currentFen, currentIndex, currentLabel})` để `GameLibrary.vue` "kéo" FEN đang xem khi trích thế cờ từ ván — **không** dùng `emit` để tránh state trùng), `views/chess/{ChessManage,ChessCourses,GameLibrary}.vue` (+tab/picker/nút "Lưu thế cờ này"), `i18n/locales/vi-VN.ts` (đổi nhãn `type_puzzle`: "Thế cờ" → **"Bài tập"**, dành "Thế cờ" cho `position` — CHỈ đổi nhãn hiển thị, KHÔNG đổi slug/dữ liệu cũ).
- **Không cần migration** cho `wiki_chess_refs`/`chess_slug_aliases`/`chess_kb_index` — cột `chess_type`/`source_type` là `VARCHAR(16)` tự do, không CHECK/enum.
- **Test mới:** `internal/chess/fen_test.go`, `chess_library_position_test.go` (fake repo kiểu embed-interface như `chess_knowledge_indexer_test.go`; phủ slug unique/rename ghi alias/resolve exact→alias→fuzzy), `chess_lookup_position_test.go` (mẫu `chess_generate_puzzle_test.go`), + case mới trong `chess_knowledge_text_test.go`. `gofmt`/`go vet`/`go build ./...` sạch; `vue-tsc --noEmit` sạch (0 lỗi).
- **Môi trường dev (Windows, ghi lại để đỡ mất công lần sau):** `go build ./...` lỗi cgo `sqlite3.h: No such file` do thiếu `CGO_CFLAGS` trỏ vào `build_sqliteshim/` (header đã có sẵn trong repo, chỉ thiếu biến môi trường) — build với `CGO_CFLAGS="-I$(pwd)/build_sqliteshim"` thì qua. Không phải lỗi do lớp cờ.

### Backlog cũ
- [x] Áp nhận diện thương hiệu Dương Sinh (`#2B3990` navy + xanh, logo) vào `frontend/` — xong WS4a (màu+logo+title). *Còn có thể làm thêm:* pattern ô cờ nền, font Roboto bundle (hiện chỉ promote trong font-stack).
- [ ] (Tùy chọn) Bật `CHESS_KB_INDEX` full stack + nối KB "Tri thức cờ vua" vào agent HLV — **runbook đã có:** `docs/chess-rag-enable.md`.
- [x] Nút "đổi tên slug" dùng `chess_slug_aliases` — xong WS-D2 cho game+puzzle (course/lesson còn lại). Position (mới) cũng có rename+alias.
- [ ] Ngân hàng thế cờ: rename cho course/lesson vẫn chưa có (nợ cũ, không phải phạm vi đợt này).
- [ ] Khi merge upstream lần tới: ưu tiên rà C1 (móc lõi) + C4 (i18n/prompt) + C6 (migration sqlite).
