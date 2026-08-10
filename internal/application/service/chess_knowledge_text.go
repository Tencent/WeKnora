package service

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// ===== Part A: expand wikilink khi embed (giữ raw cho hiển thị) =====

// chessLinkExpandRe khớp cả CHIP [[...]] lẫn NHÚNG ![[...]] (dấu ! tùy chọn).
var chessLinkExpandRe = regexp.MustCompile(`!?\[\[([^\]]+)\]\]`)

// expandChessWikilinks thay [[type/slug|nhãn]] / ![[...]] bằng VĂN BẢN CÓ NGHĨA
// để nội dung đưa đi embed không bị nhiễu cú pháp wikilink:
//   - có nhãn sau "|"          → dùng nhãn,
//   - không nhãn               → bỏ tiền tố "type/" (nếu là ref cờ) và đổi "-" → khoảng trắng.
//
// Hàm THUẦN, không đụng DB — dùng khi sinh text cho embedding (KHÔNG sửa nội dung gốc).
func expandChessWikilinks(content string) string {
	if !strings.Contains(content, "[[") {
		return content
	}
	return chessLinkExpandRe.ReplaceAllStringFunc(content, func(m string) string {
		open := strings.Index(m, "[[")
		inner := m[open+2 : len(m)-2]
		if i := strings.IndexByte(inner, '|'); i >= 0 {
			if label := strings.TrimSpace(inner[i+1:]); label != "" {
				return label
			}
			inner = inner[:i]
		}
		inner = strings.TrimSpace(inner)
		if j := strings.IndexByte(inner, '/'); j > 0 && chessRefPrefixes[inner[:j]] {
			inner = inner[j+1:]
		}
		return strings.ReplaceAll(inner, "-", " ")
	})
}

// ===== Builder văn bản tri thức cho từng đối tượng cờ (để index vào KB) =====

// gameKnowledgeTitle tạo tiêu đề "Trắng – Đen, Sự kiện Năm".
func gameKnowledgeTitle(g *types.ChessGame) string {
	white := strings.TrimSpace(g.White)
	if white == "" {
		white = "?"
	}
	black := strings.TrimSpace(g.Black)
	if black == "" {
		black = "?"
	}
	t := white + " – " + black
	if g.Event != "" {
		t += ", " + g.Event
	}
	if len(g.Date) >= 4 {
		y := g.Date[:4]
		if y >= "0000" && y <= "9999" {
			t += " " + y
		}
	}
	return t
}

// buildGameKnowledgeText sinh (tiêu đề, nội dung markdown) cho một ván cờ.
func buildGameKnowledgeText(g *types.ChessGame) (string, string) {
	title := "Ván cờ: " + gameKnowledgeTitle(g)
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", title)
	fmt.Fprintf(&b, "- Trắng: %s\n- Đen: %s\n", firstNonEmptyStr(g.White, "?"), firstNonEmptyStr(g.Black, "?"))
	if g.Result != "" {
		fmt.Fprintf(&b, "- Kết quả: %s\n", g.Result)
	}
	if g.ECO != "" {
		fmt.Fprintf(&b, "- Mã khai cuộc (ECO): %s\n", g.ECO)
	}
	if g.Event != "" {
		fmt.Fprintf(&b, "- Sự kiện: %s\n", g.Event)
	}
	if g.Date != "" {
		fmt.Fprintf(&b, "- Ngày: %s\n", g.Date)
	}
	if g.PlyCount > 0 {
		fmt.Fprintf(&b, "- Số nửa-nước: %d\n", g.PlyCount)
	}
	if strings.TrimSpace(g.PGN) != "" {
		fmt.Fprintf(&b, "\n## Biên bản (PGN)\n\n```\n%s\n```\n", strings.TrimSpace(g.PGN))
	}
	return title, b.String()
}

// buildPuzzleKnowledgeText sinh (tiêu đề, nội dung) cho một thế cờ/bài tập.
func buildPuzzleKnowledgeText(p *types.ChessPuzzle) (string, string) {
	name := firstNonEmptyStr(p.Title, p.Slug)
	title := "Thế cờ: " + name
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", title)
	if p.Theme != "" {
		fmt.Fprintf(&b, "- Chủ đề: %s\n", p.Theme)
	}
	if p.Difficulty != "" {
		fmt.Fprintf(&b, "- Độ khó: %s\n", p.Difficulty)
	}
	if p.FEN != "" {
		fmt.Fprintf(&b, "- Thế cờ (FEN): `%s`\n", p.FEN)
	}
	if p.Solution != "" {
		fmt.Fprintf(&b, "- Lời giải: %s\n", p.Solution)
	}
	if p.Source != "" {
		fmt.Fprintf(&b, "- Nguồn: %s\n", p.Source)
	}
	return title, b.String()
}

// buildLessonKnowledgeText sinh (tiêu đề, nội dung) cho một bài giảng. Nội dung
// bài giảng do người dùng viết (có thể chứa wikilink cờ) → expand để embed sạch.
func buildLessonKnowledgeText(l *types.ChessLesson) (string, string) {
	name := firstNonEmptyStr(l.Title, l.Slug)
	title := "Bài giảng: " + name
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", title)
	if body := strings.TrimSpace(expandChessWikilinks(l.Content)); body != "" {
		b.WriteString(body)
		b.WriteString("\n")
	}
	if l.FEN != "" {
		fmt.Fprintf(&b, "\n- Thế cờ (FEN): `%s`\n", l.FEN)
	}
	if strings.TrimSpace(l.PGN) != "" {
		fmt.Fprintf(&b, "\n## Ván minh họa (PGN)\n\n```\n%s\n```\n", strings.TrimSpace(l.PGN))
	}
	return title, b.String()
}

// chessLevelLabel dịch mã cấp độ 6 bậc Dương Sinh sang nhãn tiếng Việt đầy đủ
// để văn bản index đọc tự nhiên (thay vì mã "tot"/"vua" trần).
func chessLevelLabel(level string) string {
	switch level {
	case "tot":
		return "Tốt"
	case "ma":
		return "Mã"
	case "tuong":
		return "Tượng"
	case "xe":
		return "Xe"
	case "hau":
		return "Hậu"
	case "vua":
		return "Vua"
	default:
		return level
	}
}

// buildPositionKnowledgeText sinh (tiêu đề, nội dung) cho một thế cờ trong Ngân
// hàng thế cờ. Khác buildPuzzleKnowledgeText (bài TẬP, có lời giải): position là
// thế cờ THAM CHIẾU để dạy/trích dẫn, nên phần chú giải (có thể chứa wikilink)
// là nội dung chính — expand giống bài giảng để không nhiễu cú pháp khi embed.
func buildPositionKnowledgeText(p *types.ChessPosition) (string, string) {
	name := firstNonEmptyStr(p.Title, p.Slug)
	title := "Thế cờ: " + name
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", title)
	if p.Category != "" {
		fmt.Fprintf(&b, "- Phân loại: %s\n", p.Category)
	}
	if p.Level != "" {
		fmt.Fprintf(&b, "- Cấp độ: %s\n", chessLevelLabel(p.Level))
	}
	if p.ECO != "" {
		fmt.Fprintf(&b, "- Mã khai cuộc (ECO): %s\n", p.ECO)
	}
	if p.Assessment != "" {
		fmt.Fprintf(&b, "- Đánh giá: %s\n", p.Assessment)
	}
	if p.FEN != "" {
		fmt.Fprintf(&b, "- Thế cờ (FEN): `%s`\n", p.FEN)
	}
	if p.Tags != "" {
		fmt.Fprintf(&b, "- Thẻ: %s\n", p.Tags)
	}
	if body := strings.TrimSpace(expandChessWikilinks(p.Annotation)); body != "" {
		b.WriteString("\n")
		b.WriteString(body)
		b.WriteString("\n")
	}
	if p.Source != "" {
		fmt.Fprintf(&b, "\n- Nguồn: %s\n", p.Source)
	}
	return title, b.String()
}

// buildBookKnowledgeText sinh (tiêu đề, nội dung) cho một cuốn sách — nội dung
// gồm metadata thư mục + mô tả + MỤC LỤC (tiêu đề chương, gộp theo Part) để
// agent biết sách có những gì mà không cần nạp toàn văn từng chương (từng
// chương được index RIÊNG qua buildChapterKnowledgeText).
func buildBookKnowledgeText(b *types.ChessBook, chapters []*types.ChessBookChapter) (string, string) {
	name := firstNonEmptyStr(b.Title, b.Slug)
	title := "Sách: " + name
	var out strings.Builder
	fmt.Fprintf(&out, "# %s\n\n", title)
	if b.Subtitle != "" {
		fmt.Fprintf(&out, "*%s*\n\n", b.Subtitle)
	}
	if b.Author != "" {
		fmt.Fprintf(&out, "- Tác giả: %s\n", b.Author)
	}
	if b.Translator != "" {
		fmt.Fprintf(&out, "- Dịch giả: %s\n", b.Translator)
	}
	if b.Publisher != "" {
		fmt.Fprintf(&out, "- Nhà xuất bản: %s\n", b.Publisher)
	}
	if b.Year != "" {
		fmt.Fprintf(&out, "- Năm: %s\n", b.Year)
	}
	if b.Level != "" {
		fmt.Fprintf(&out, "- Cấp độ: %s\n", chessLevelLabel(b.Level))
	}
	if b.Phase != "" {
		fmt.Fprintf(&out, "- Giai đoạn: %s\n", b.Phase)
	}
	if b.ECO != "" {
		fmt.Fprintf(&out, "- Mã khai cuộc (ECO): %s\n", b.ECO)
	}
	if b.Tags != "" {
		fmt.Fprintf(&out, "- Thẻ: %s\n", b.Tags)
	}
	if body := strings.TrimSpace(b.Description); body != "" {
		out.WriteString("\n")
		out.WriteString(body)
		out.WriteString("\n")
	}
	if len(chapters) > 0 {
		out.WriteString("\n## Mục lục\n\n")
		lastPart := "\x00" // giá trị canh gác không khớp Part thật để in tiêu đề Phần đầu tiên
		for _, ch := range chapters {
			if ch.Part != lastPart {
				if ch.Part != "" {
					fmt.Fprintf(&out, "\n%s\n", ch.Part)
				}
				lastPart = ch.Part
			}
			fmt.Fprintf(&out, "- %s\n", firstNonEmptyStr(ch.Title, ch.Slug))
		}
	}
	return title, out.String()
}

// buildChapterKnowledgeText sinh (tiêu đề, nội dung) cho một chương sách.
// Tiêu đề gắn tên sách để agent trích dẫn rõ nguồn ("Chương: <sách> — <chương>").
// Nội dung do người soạn viết (có thể chứa wikilink cờ) → expand để embed sạch,
// cùng khuôn buildLessonKnowledgeText/buildPositionKnowledgeText.
func buildChapterKnowledgeText(book *types.ChessBook, ch *types.ChessBookChapter) (string, string) {
	bookName := firstNonEmptyStr(book.Title, book.Slug)
	chapName := firstNonEmptyStr(ch.Title, ch.Slug)
	title := "Chương: " + bookName + " — " + chapName
	var out strings.Builder
	fmt.Fprintf(&out, "# %s\n\n", title)
	if ch.Part != "" {
		fmt.Fprintf(&out, "- Phần: %s\n", ch.Part)
	}
	if ch.Level != "" {
		fmt.Fprintf(&out, "- Cấp độ: %s\n", chessLevelLabel(ch.Level))
	}
	if ch.FEN != "" {
		fmt.Fprintf(&out, "- Thế cờ (FEN): `%s`\n", ch.FEN)
	}
	if body := strings.TrimSpace(expandChessWikilinks(ch.Content)); body != "" {
		out.WriteString("\n")
		out.WriteString(body)
		out.WriteString("\n")
	}
	return title, out.String()
}

func firstNonEmptyStr(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}
