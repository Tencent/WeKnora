# AGENTS.md — WeKnora_chess (Cờ vua Dương Sinh)

> Hợp đồng vận hành cho mọi AI coding agent (Claude Code, Cursor, Aider, Codex…) làm việc trong repo này.
> **Đọc file này TRƯỚC khi sửa code.** Đây là nguồn sự thật về mục tiêu, hiện trạng tùy biến và ranh giới.

---

## 1. Đây là gì

Fork của [Tencent/WeKnora](https://github.com/Tencent/WeKnora) (knowledge platform LLM: RAG + ReAct Agent + Auto-Wiki), **đã được xây thêm cả một lớp cờ vua (chess layer)** xuyên suốt backend Go, frontend Vue và CSDL — phục vụ Công ty CP Cờ vua Dương Sinh (slogan *"Vui trí tuệ"*).

- **Phiên bản nền:** WeKnora **v0.6.2** (xem `VERSION`, `CHANGELOG.md`).
- **Nhánh chính:** `main`. **Production:** `weknora.covuaduongsinh.com`.
- **Định hướng:** cờ vua là **công cụ giáo dục** cho trẻ em; ưu tiên **phong trào hơn thành tích**. Khi phân vân hướng sản phẩm, chọn cái phục vụ học viên nhỏ tuổi và HLV phong trào.

> ⚠️ **Đừng nhầm với Arkon.** Arkon (`app.covuaduongsinh.com`, repo `arkon_duongsinh`) là hệ KB **Python/FastAPI + Gemini** trên Hetzner — dự án KHÁC HẲN. Repo này là **Go + Vue**. Không trộn lệnh deploy/schema/cấu trúc giữa hai dự án. Vận hành Arkon dùng skill `arkon-deploy`.

---

## 2. Ngăn xếp

| Lớp | Công nghệ |
|---|---|
| Backend | **Go** (Go 1.26.x) — `internal/`, `cmd/`, `config/` |
| Frontend | **Vue + TypeScript** — `frontend/` (và `web/`) |
| Đọc tài liệu | **Python** (gRPC) — `docreader/` |
| Engine cờ | **Arasan** (UCI) bọc HTTP sidecar — `internal/chess/`, `docker/chess-engine/` |
| CSDL + vector | PostgreSQL + **pgvector** |
| Hạ tầng | **Docker Compose** (+ overlay `docker-compose.chess.yml`), systemd (`scripts/deploy/weknora-chess.service`) |

---

## 3. Lớp cờ vua đã tùy biến (BẢN ĐỒ — đọc kỹ trước khi đụng vào)

Đây là phần tùy biến lớn nhất so với upstream. Khái niệm chính: **Game** (ván cờ), **Position** (thế cờ/FEN), **Lesson** (bài giảng), **Course** (khóa học), **Puzzle** (bài tập), **Shelf/Book/Chapter** (kệ/sách/chương — Thư viện sách cờ vua, soạn thảo nội bộ), và **Chess Wikilink/Ref** (liên kết chéo kiểu Obsidian `[[game/<slug>|Nhãn]]`, gồm cả `[[book/<slug>]]`/`[[chapter/<slug>]]`).

| Vùng | File chính | Vai trò |
|---|---|---|
| Engine | `internal/chess/` (`board.go`, `engine.go`, `uci_engine.go`, `http_engine.go`) | Bọc Arasan qua UCI/HTTP |
| Agent tools | `internal/agent/tools/chess_*.go` | 7 tool: `chess_analyze_position`, `chess_best_move`, `chess_evaluate_game`, `chess_explain_move`, `chess_lookup_opening`, `chess_generate_puzzle`, `chess_lookup_position` (+ `chess_common.go`) |
| Repository | `internal/application/repository/chess_*`, `wiki_chess_ref.go` | course, kb_index, library (game/puzzle/position/**book/shelf/chapter**), slug_alias, wiki ref |
| Service | `internal/application/service/chess_*` | course, knowledge_indexer, knowledge_text, library, library_position, **library_book**, resolve, slug |
| Handler (API) | `internal/handler/chess_*` | course, library, position, **book**, ref — vd `GET /api/v1/chess/refs/search?q=` |
| Types | `internal/types/...chess_*`, `wiki_chess_ref.go` | kiểu dữ liệu & interfaces |
| Migrations | `migrations/versioned/000062`–`000071` | courses, games_puzzles, slugs, wiki_chess_refs, course_slug, refs_source_type, slug_aliases, kb_index, chess_positions, **chess_books (kệ/sách/chương/ảnh/phiên bản)** |
| Frontend | `frontend/src/views/chess/` (ChessCourses, ChessManage, GameLibrary, PuzzleBank, PositionBank, **BookLibrary, BookPrint** + components), `views/chat/components/tool-results/ChessBoardDisplay.vue`, `api/chess/`, `stores/chessWikiDraft.ts`, `utils/chessBlocks.ts`, `utils/chessRef.ts` | UI quản lý cờ, bàn cờ tương tác, wikilink, **thư viện sách** |
| Agent cấu hình | `config/builtin_agents.yaml` → agent `builtin-chess-coach` ("HLV Cờ vua") | system prompt tiếng Việt + 6 chess tools |
| Docker | `docker-compose.chess.yml`, `docker/Dockerfile.chess-engine`, `docker/chess-engine/uci_http_bridge.py` | overlay engine |
| Scripts/Docs | `scripts/deploy/weknora-chess.service`, `scripts/seed_chess_wikilink_demo.*`, `docs/chess-wikilink-demo.md` | deploy + demo |

**Biến môi trường lớp cờ (trong `.env`):**
- `WEKNORA_CHESS_ENABLED`, `WEKNORA_CHESS_MODE=http`, `WEKNORA_CHESS_ENGINE_ENDPOINT`, `WEKNORA_CHESS_DEFAULT_DEPTH`, `WEKNORA_CHESS_TIMEOUT_SEC` — engine.
- `CHESS_KB_INDEX` — **mặc định TẮT**. Bật để index ván/thế/bài giảng vào KB "Tri thức cờ vua" cho RAG (cần embedding + vector store + worker; xem `docs/chess-wikilink-demo.md` Pha 3).

---

## 4. Lệnh hay dùng

### Chạy bằng Docker (kèm overlay cờ)
```bash
# Core + engine cờ Arasan
docker compose -f docker-compose.yml -f docker-compose.chess.yml up -d
docker compose ps
```
> Overlay mount `config/builtin_agents.yaml` từ host → chỉnh agent "HLV Cờ vua" **không cần rebuild**.

### Phát triển nhanh (hot-reload, không rebuild image)
```bash
make dev-start       # hạ tầng
make dev-app         # backend Go (Air hot-reload)
make dev-frontend    # frontend Vue
```

### Service URLs
| Service | URL |
|---|---|
| Web UI | `http://localhost` |
| Backend API | `http://localhost:8080` |

---

## 5. Quy ước code & commit

- **`gofmt`** trước commit; lint theo `.golangci.yml`.
- **Conventional Commits**: `feat:` / `fix:` / `docs:` / `test:` / `refactor:` / `chore:`. Ví dụ: `feat(chess): them tool tra cuu khai cuoc`.
- Thay đổi schema → **thêm migration mới** trong `migrations/versioned/` (đánh số tiếp theo, có cả `.up.sql` và `.down.sql`). Không sửa migration cũ đã chạy production.
- Frontend theo lint/format có sẵn trong `frontend/`.

---

## 6. Ranh giới tùy biến & chiến lược sync upstream

Lớp cờ đã đụng sâu vào `internal/` và `migrations/` → **`git merge upstream/main` gần như chắc chắn có conflict**. Vì vậy:

### Nguyên tắc giữ "diff sạch" với upstream
- **Code cờ để RIÊNG** trong các file `*chess*` / thư mục `internal/chess/`, `frontend/src/views/chess/` → dễ giữ qua merge.
- Khi buộc phải **sửa file dùng chung của upstream** (router, đăng ký tool, store frontend, schema chung): giữ thay đổi **tối thiểu, khoanh vùng rõ**, và **GHI NGAY vào `.claude/memory/04-nhat-ky-tuy-bien.md`** (file gì, vì sao, điểm dễ conflict).
- Mọi migration cờ đánh số > upstream để tránh đụng số.

### ❌ Không làm khi chưa được duyệt
- Xóa attribution Tencent / đổi LICENSE (MIT — phải giữ ghi công).
- `git reset --hard`, ép `git pull`/`merge` khi đang conflict, drop bảng, xóa volume trên production.
- Commit secret (key LLM/Gemini, mật khẩu DB, API key tenant) — dùng `.env` (đã gitignore). KB cờ lưu API key **mã hóa**, không đọc từ DB.

---

## 7. Bảo mật

- Không expose service ra Internet công cộng nếu không cần; cấu hình firewall. WeKnora có đăng nhập từ v0.1.3.
- API key & credential mã hóa AES-256-GCM ở tầng ứng dụng — không bypass.
- Không in/dán/commit secret ra log hay chat.

---

## 8. Bối cảnh (memory files)

Đọc thêm trong `.claude/memory/` trước khi ra quyết định:
- `01-du-an-duongsinh.md` — công ty, triết lý, lộ trình 6 cấp, thương hiệu.
- `02-mien-co-vua.md` — domain cờ vua, thuật ngữ song ngữ, xử lý FEN/PGN.
- `03-kien-truc-weknora.md` — kiến trúc nền + lớp cờ chi tiết hơn.
- `04-nhat-ky-tuy-bien.md` — **inventory tùy biến vs upstream** (cập nhật mỗi khi sửa file dùng chung).
- `05-playbook-knowledge-base.md` — vận hành: agent HLV, chess tools, bật RAG cờ, dựng KB.

## 9. Khi không chắc
Phân vân giữa "sửa code cờ riêng" (an toàn) vs "sửa file dùng chung upstream" (rủi ro merge) → **hỏi đúng 1 câu** rồi mới làm. Ưu tiên thay đổi nhỏ, đảo ngược được, có ghi nhật ký.
