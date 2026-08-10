package handler

import (
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// ChessRefHandler phục vụ TÌM KIẾM HỢP NHẤT tham chiếu cờ (game/puzzle/lesson/course)
// cho autocomplete wikilink khi người dùng gõ "[[". Gộp dữ liệu từ hai service
// (kho ván/bài tập và khóa học/bài giảng) thành một danh sách gợi ý thống nhất.
type ChessRefHandler struct {
	library interfaces.ChessLibraryService
	course  interfaces.ChessCourseService
}

// NewChessRefHandler tạo handler tìm kiếm tham chiếu cờ.
func NewChessRefHandler(library interfaces.ChessLibraryService, course interfaces.ChessCourseService) *ChessRefHandler {
	return &ChessRefHandler{library: library, course: course}
}

// SearchRefs GET /chess/refs/search?q=&type=&limit=
// Trả [{type, slug, ref, title, subtitle}] để autocomplete khi gõ "[[".
// - q: từ khóa (rỗng = trả các mục mới nhất mỗi loại).
// - type: lọc một loại (game|puzzle|lesson|course); rỗng = mọi loại.
// - limit: số mục tối đa MỖI loại (mặc định 6, tối đa 25).
func (h *ChessRefHandler) SearchRefs(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := types.MustTenantIDFromContext(ctx)
	q := strings.TrimSpace(c.Query("q"))
	typeFilter := strings.TrimSpace(c.Query("type"))
	perType := 6
	if v, err := strconv.Atoi(c.Query("limit")); err == nil && v > 0 && v <= 25 {
		perType = v
	}
	want := func(t string) bool { return typeFilter == "" || typeFilter == t }

	items := make([]types.ChessRefSearchItem, 0, perType*4)

	if want(types.ChessRefTypeGame) {
		games, _ := h.library.ListGames(ctx, tenantID, types.ChessGameFilter{Keyword: q})
		for i, g := range games {
			if i >= perType {
				break
			}
			items = append(items, types.ChessRefSearchItem{
				Type:     types.ChessRefTypeGame,
				Slug:     g.Slug,
				Ref:      types.ChessRefTypeGame + "/" + g.Slug,
				Title:    gameDisplayTitle(g),
				Subtitle: strings.TrimSpace(g.ECO + " " + g.Event),
			})
		}
	}
	if want(types.ChessRefTypePuzzle) {
		puzzles, _ := h.library.ListPuzzles(ctx, tenantID, types.ChessPuzzleFilter{Keyword: q})
		for i, p := range puzzles {
			if i >= perType {
				break
			}
			items = append(items, types.ChessRefSearchItem{
				Type:     types.ChessRefTypePuzzle,
				Slug:     p.Slug,
				Ref:      types.ChessRefTypePuzzle + "/" + p.Slug,
				Title:    firstNonEmpty(p.Title, p.Slug),
				Subtitle: strings.TrimSpace(p.Theme + " " + p.Difficulty),
			})
		}
	}
	if want(types.ChessRefTypeLesson) {
		lessons, _ := h.course.SearchLessons(ctx, tenantID, q, perType)
		for _, l := range lessons {
			items = append(items, types.ChessRefSearchItem{
				Type:  types.ChessRefTypeLesson,
				Slug:  l.Slug,
				Ref:   types.ChessRefTypeLesson + "/" + l.Slug,
				Title: firstNonEmpty(l.Title, l.Slug),
			})
		}
	}
	if want(types.ChessRefTypeCourse) {
		courses, _ := h.course.ListCourses(ctx, tenantID)
		n := 0
		for _, co := range courses {
			if n >= perType {
				break
			}
			if q != "" && !containsFold(co.Title, q) && !containsFold(co.Slug, q) {
				continue
			}
			items = append(items, types.ChessRefSearchItem{
				Type:     types.ChessRefTypeCourse,
				Slug:     co.Slug,
				Ref:      types.ChessRefTypeCourse + "/" + co.Slug,
				Title:    firstNonEmpty(co.Title, co.Slug),
				Subtitle: co.Level,
			})
			n++
		}
	}
	if want(types.ChessRefTypePosition) {
		positions, _ := h.library.ListPositions(ctx, tenantID, types.ChessPositionFilter{Keyword: q})
		for i, p := range positions {
			if i >= perType {
				break
			}
			items = append(items, types.ChessRefSearchItem{
				Type:     types.ChessRefTypePosition,
				Slug:     p.Slug,
				Ref:      types.ChessRefTypePosition + "/" + p.Slug,
				Title:    firstNonEmpty(p.Title, p.Slug),
				Subtitle: strings.TrimSpace(p.Category + " " + p.ECO),
			})
		}
	}
	if want(types.ChessRefTypeBook) {
		books, _ := h.library.ListBooks(ctx, tenantID, types.ChessBookFilter{Keyword: q})
		for i, b := range books {
			if i >= perType {
				break
			}
			items = append(items, types.ChessRefSearchItem{
				Type:     types.ChessRefTypeBook,
				Slug:     b.Slug,
				Ref:      types.ChessRefTypeBook + "/" + b.Slug,
				Title:    firstNonEmpty(b.Title, b.Slug),
				Subtitle: strings.TrimSpace(b.Author + " " + b.Year),
			})
		}
	}
	if want(types.ChessRefTypeChapter) {
		chapters, _ := h.library.SearchChapters(ctx, tenantID, q, perType)
		for _, ch := range chapters {
			items = append(items, types.ChessRefSearchItem{
				Type:     types.ChessRefTypeChapter,
				Slug:     ch.Slug,
				Ref:      types.ChessRefTypeChapter + "/" + ch.Slug,
				Title:    firstNonEmpty(ch.Title, ch.Slug),
				Subtitle: ch.Part,
			})
		}
	}

	chessOK(c, items)
}

// gameDisplayTitle tạo nhãn "Trắng – Đen, Sự kiện Năm" (đồng bộ với gameTitle ở FE).
func gameDisplayTitle(g *types.ChessGame) string {
	t := firstNonEmpty(g.White, "?") + " – " + firstNonEmpty(g.Black, "?")
	if g.Event != "" {
		t += ", " + g.Event
	}
	if len(g.Date) >= 4 {
		if y := g.Date[:4]; isYear(y) {
			t += " " + y
		}
	}
	return t
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func isYear(s string) bool {
	if len(s) != 4 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func containsFold(haystack, needle string) bool {
	return strings.Contains(strings.ToLower(haystack), strings.ToLower(needle))
}
