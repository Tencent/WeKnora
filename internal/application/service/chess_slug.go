package service

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"

	"github.com/Tencent/WeKnora/internal/types"
)

// Sinh slug thân thiện cho đối tượng cờ vua (ván/thế cờ/bài giảng) làm đích
// wikilink [[game/<slug>]]. Slug duy nhất theo tenant cho mỗi loại; gán một lần
// khi tạo và giữ ổn định sau đó (đổi slug = đổi đích link, như đổi tên trang wiki).

// diacriticFold tách tổ hợp (NFD) → bỏ dấu thanh/dấu phụ (combining marks Mn) →
// NFC. Khử dấu tiếng Việt ROBUST bất kể đầu vào ở dạng NFC hay NFD (tránh lệ
// thuộc bảng ký tự dựng sẵn vốn dễ trật khi normalize khác nhau).
var diacriticFold = transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)

// foldVN bỏ dấu tiếng Việt: xử lý đ/Đ (không phải tổ hợp dấu) trước rồi tách dấu.
func foldVN(s string) string {
	s = strings.NewReplacer("đ", "d", "Đ", "D", "ð", "d").Replace(s)
	out, _, err := transform.String(diacriticFold, s)
	if err != nil {
		return s
	}
	return out
}

// slugifyChess: lowercase, bỏ dấu tiếng Việt, ký tự ngoài [a-z0-9] → "-",
// gộp nhiều "-", cắt độ dài. Trả "" nếu không còn ký tự hợp lệ.
func slugifyChess(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = foldVN(s)
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 60 {
		out = strings.Trim(out[:60], "-")
	}
	return out
}

// gameSlugBase: "trắng-đen[-giải][-năm]".
func gameSlugBase(g *types.ChessGame) string {
	base := slugifyChess(g.White + "-" + g.Black)
	if ev := slugifyChess(g.Event); ev != "" {
		base = strings.Trim(base+"-"+ev, "-")
	}
	year := strings.TrimSpace(g.Date)
	if len(year) >= 4 {
		if y := slugifyChess(year[:4]); y != "" {
			base = strings.Trim(base+"-"+y, "-")
		}
	}
	return base
}

// puzzleSlugBase: tiêu đề, hoặc chủ đề.
func puzzleSlugBase(p *types.ChessPuzzle) string {
	if s := slugifyChess(p.Title); s != "" {
		return s
	}
	return slugifyChess(p.Theme)
}

// lessonSlugBase: tiêu đề bài giảng.
func lessonSlugBase(l *types.ChessLesson) string {
	return slugifyChess(l.Title)
}

// positionSlugBase: tiêu đề thế cờ, hoặc phân loại nếu thiếu tiêu đề, hoặc
// tiền tố "pos" nếu cả hai đều rỗng (ensureUniqueChessSlug tự nối id8 sau).
func positionSlugBase(p *types.ChessPosition) string {
	if s := slugifyChess(p.Title); s != "" {
		return s
	}
	if s := slugifyChess(p.Category); s != "" {
		return s
	}
	return "pos"
}

// courseSlugBase: tiêu đề khóa học.
func courseSlugBase(c *types.ChessCourse) string {
	return slugifyChess(c.Title)
}

// id8 lấy 8 hex đầu của UUID (đã bỏ dấu "-") làm hậu tố/giá trị dự phòng.
func id8(uuid string) string {
	h := strings.ReplaceAll(uuid, "-", "")
	if len(h) > 8 {
		return h[:8]
	}
	return h
}

// ensureUniqueChessSlug trả về slug duy nhất theo tenant: thử base, base-2,
// base-3… ; nếu base rỗng hoặc đụng quá nhiều thì dùng/đính kèm id8 (chắc chắn
// duy nhất). exists kiểm tra slug đã bị hàng khác chiếm chưa.
func ensureUniqueChessSlug(ctx context.Context, tenantID uint64, base, fallbackID string,
	exists func(ctx context.Context, tenantID uint64, slug string) (bool, error),
) (string, error) {
	suffix := id8(fallbackID)
	if base == "" {
		base = suffix
	}
	cand := base
	for i := 2; i <= 50; i++ {
		taken, err := exists(ctx, tenantID, cand)
		if err != nil {
			return "", err
		}
		if !taken {
			return cand, nil
		}
		cand = fmt.Sprintf("%s-%d", base, i)
	}
	// Quá nhiều trùng tên → nối id8 đảm bảo duy nhất.
	return base + "-" + suffix, nil
}
