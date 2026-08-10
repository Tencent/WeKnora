# CLAUDE.md

> File memory dự án cho **Claude Code**. Nguồn sự thật chính về repo là **`AGENTS.md`** — đọc nó trước.
> File này bổ sung quy ước riêng cho Claude + nạp (import) các memory file bối cảnh.

## Nguồn chính
@AGENTS.md

## Bối cảnh dự án (memory imports)
@.claude/memory/01-du-an-duongsinh.md
@.claude/memory/02-mien-co-vua.md
@.claude/memory/03-kien-truc-weknora.md
@.claude/memory/04-nhat-ky-tuy-bien.md
@.claude/memory/05-playbook-knowledge-base.md

---

## Quy ước riêng khi làm việc với Thầy Tường
- **Ngôn ngữ:** trả lời **tiếng Việt**, kèm thuật ngữ cờ vua/tiếng Anh khi cần (opening, endgame, FEN, PGN, fork, pin…). Giữ tiếng Anh cho code, lệnh, tên biến, log.
- **Phong cách:** thực chiến, đi thẳng vấn đề. Ưu tiên **giao file/kết quả sẵn dùng** hơn hướng dẫn nhiều bước.
- **Thương hiệu Dương Sinh:** UI/ấn phẩm phải đúng nhận diện — màu `#2B3990` (navy) + xanh (teal/blue), font Roboto/Calibri, họa tiết ô vuông cờ, logo. **KHÔNG dùng cam/gold** (đính chính so với ghi chú cũ — xem `01-du-an-duongsinh.md`).

## Trước khi sửa code, tự hỏi
1. Đây là **code cờ riêng** (`*chess*`, `internal/chess/`, `frontend/src/views/chess/`) — an toàn, hay **file dùng chung của upstream** — rủi ro conflict khi merge?
2. Có làm khó `git merge upstream/main` về sau không?
3. Nếu đụng file dùng chung hoặc schema → đã ghi `04-nhat-ky-tuy-bien.md` chưa?
4. Sửa DB → đã thêm migration mới (đánh số tiếp, có `.up`/`.down`) thay vì sửa migration cũ chưa?

## Hiện trạng cần nhớ (đừng làm lại từ đầu)
- Đã có agent **`builtin-chess-coach` ("HLV Cờ vua")** trong `config/builtin_agents.yaml` + 7 tool cờ (`chess_analyze_position`, `chess_best_move`, `chess_evaluate_game`, `chess_explain_move`, `chess_lookup_opening`, `chess_generate_puzzle`, `chess_lookup_position`).
- Đã có engine **Arasan** (overlay `docker-compose.chess.yml`) và **Chess Wikilink** (`[[game/<slug>|Nhãn]]`, autocomplete, fuzzy slug) cho 7 loại thực thể: game/puzzle/lesson/course/position/**book/chapter** (Thư viện sách — xem mục 3).
- RAG cờ qua gate **`CHESS_KB_INDEX`** — đã **BẬT ở production** (chi tiết vận hành/chẩn đoán: `04-nhat-ky-tuy-bien.md` mục "Đợt sửa RAG…", runbook `docs/chess-rag-enable.md`). Sách chỉ index khi `status="published"`.

## Đừng nhầm
- Repo này = **WeKnora_chess (Go + Vue)**, production `weknora.covuaduongsinh.com`. KHÁC **Arkon** (Python/FastAPI + Gemini, `app.covuaduongsinh.com`). Vận hành Arkon dùng skill `arkon-deploy`.

## Luôn nhớ
- `gofmt` + Conventional Commits. Ưu tiên `make dev-*`. Không commit secret. Không expose service công cộng tùy tiện.
