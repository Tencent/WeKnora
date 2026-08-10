package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// ChessLibraryHandler xử lý API kho ván đấu & ngân hàng bài tập cờ vua.
type ChessLibraryHandler struct {
	service interfaces.ChessLibraryService
}

// NewChessLibraryHandler tạo handler kho ván & bài tập.
func NewChessLibraryHandler(service interfaces.ChessLibraryService) *ChessLibraryHandler {
	return &ChessLibraryHandler{service: service}
}

func chessOK(c *gin.Context, data interface{}) {
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}

func chessFail(c *gin.Context, code int, err error) {
	c.JSON(code, gin.H{"success": false, "error": err.Error()})
}

// ---- Ván đấu ----

// ListGames GET /chess/games?white=&black=&eco=&result=
func (h *ChessLibraryHandler) ListGames(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := types.MustTenantIDFromContext(ctx)
	games, err := h.service.ListGames(ctx, tenantID, types.ChessGameFilter{
		White: c.Query("white"), Black: c.Query("black"),
		ECO: c.Query("eco"), Result: c.Query("result"),
	})
	if err != nil {
		chessFail(c, http.StatusInternalServerError, err)
		return
	}
	chessOK(c, games)
}

// GetGame GET /chess/games/:id
func (h *ChessLibraryHandler) GetGame(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := types.MustTenantIDFromContext(ctx)
	g, err := h.service.GetGame(ctx, tenantID, c.Param("id"))
	if err != nil {
		chessFail(c, http.StatusNotFound, err)
		return
	}
	chessOK(c, g)
}

// GetGameBySlug GET /chess/games/by-slug/:slug — giải mã wikilink [[game/<slug>]].
func (h *ChessLibraryHandler) GetGameBySlug(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := types.MustTenantIDFromContext(ctx)
	g, err := h.service.GetGameBySlug(ctx, tenantID, c.Param("slug"))
	if err != nil {
		chessFail(c, http.StatusNotFound, err)
		return
	}
	chessOK(c, g)
}

// GetGameBacklinks GET /chess/games/by-slug/:slug/backlinks — trang wiki trỏ tới ván.
func (h *ChessLibraryHandler) GetGameBacklinks(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := types.MustTenantIDFromContext(ctx)
	links, err := h.service.GetGameBacklinks(ctx, tenantID, c.Param("slug"))
	if err != nil {
		chessFail(c, http.StatusInternalServerError, err)
		return
	}
	chessOK(c, links)
}

type gameBody struct {
	White  string `json:"white"`
	Black  string `json:"black"`
	Result string `json:"result"`
	ECO    string `json:"eco"`
	Event  string `json:"event"`
	Date   string `json:"date"`
	PGN    string `json:"pgn"`
}

// CreateGame POST /chess/games
func (h *ChessLibraryHandler) CreateGame(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := types.MustTenantIDFromContext(ctx)
	var b gameBody
	if err := c.ShouldBindJSON(&b); err != nil {
		chessFail(c, http.StatusBadRequest, err)
		return
	}
	g, err := h.service.CreateGame(ctx, &types.ChessGame{
		TenantID: tenantID, White: b.White, Black: b.Black, Result: b.Result,
		ECO: b.ECO, Event: b.Event, Date: b.Date, PGN: b.PGN,
	})
	if err != nil {
		chessFail(c, http.StatusBadRequest, err)
		return
	}
	chessOK(c, g)
}

// UpdateGame PUT /chess/games/:id
func (h *ChessLibraryHandler) UpdateGame(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := types.MustTenantIDFromContext(ctx)
	var b gameBody
	if err := c.ShouldBindJSON(&b); err != nil {
		chessFail(c, http.StatusBadRequest, err)
		return
	}
	g, err := h.service.UpdateGame(ctx, &types.ChessGame{
		ID: c.Param("id"), TenantID: tenantID, White: b.White, Black: b.Black,
		Result: b.Result, ECO: b.ECO, Event: b.Event, Date: b.Date, PGN: b.PGN,
	})
	if err != nil {
		chessFail(c, http.StatusBadRequest, err)
		return
	}
	chessOK(c, g)
}

type slugBody struct {
	Slug string `json:"slug"`
}

// RenameGameSlug PUT /chess/games/:id/slug {slug} — đổi slug ván, giữ link cũ qua alias.
func (h *ChessLibraryHandler) RenameGameSlug(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := types.MustTenantIDFromContext(ctx)
	var b slugBody
	if err := c.ShouldBindJSON(&b); err != nil {
		chessFail(c, http.StatusBadRequest, err)
		return
	}
	g, err := h.service.RenameGameSlug(ctx, tenantID, c.Param("id"), b.Slug)
	if err != nil {
		chessFail(c, http.StatusBadRequest, err)
		return
	}
	chessOK(c, g)
}

// RenamePuzzleSlug PUT /chess/puzzles/:id/slug {slug} — đổi slug bài tập, giữ link cũ.
func (h *ChessLibraryHandler) RenamePuzzleSlug(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := types.MustTenantIDFromContext(ctx)
	var b slugBody
	if err := c.ShouldBindJSON(&b); err != nil {
		chessFail(c, http.StatusBadRequest, err)
		return
	}
	p, err := h.service.RenamePuzzleSlug(ctx, tenantID, c.Param("id"), b.Slug)
	if err != nil {
		chessFail(c, http.StatusBadRequest, err)
		return
	}
	chessOK(c, p)
}

// DeleteGame DELETE /chess/games/:id
func (h *ChessLibraryHandler) DeleteGame(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := types.MustTenantIDFromContext(ctx)
	if err := h.service.DeleteGame(ctx, tenantID, c.Param("id")); err != nil {
		chessFail(c, http.StatusInternalServerError, err)
		return
	}
	chessOK(c, gin.H{"deleted": true})
}

// ImportGames POST /chess/games/import {pgn}
func (h *ChessLibraryHandler) ImportGames(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := types.MustTenantIDFromContext(ctx)
	var b struct {
		PGN string `json:"pgn"`
	}
	if err := c.ShouldBindJSON(&b); err != nil {
		chessFail(c, http.StatusBadRequest, err)
		return
	}
	count, err := h.service.ImportGames(ctx, tenantID, b.PGN)
	if err != nil {
		chessFail(c, http.StatusBadRequest, err)
		return
	}
	chessOK(c, gin.H{"imported": count})
}

// ExportGames GET /chess/games/export?white=&black=&eco=&result= — trả PGN nhiều ván.
func (h *ChessLibraryHandler) ExportGames(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := types.MustTenantIDFromContext(ctx)
	pgn, err := h.service.ExportGamesPGN(ctx, tenantID, types.ChessGameFilter{
		White: c.Query("white"), Black: c.Query("black"),
		ECO: c.Query("eco"), Result: c.Query("result"),
	})
	if err != nil {
		chessFail(c, http.StatusInternalServerError, err)
		return
	}
	chessOK(c, gin.H{"pgn": pgn})
}

// ---- Bài tập ----

// ExportPuzzles GET /chess/puzzles/export?theme=&difficulty= — danh sách bài tập (JSON).
func (h *ChessLibraryHandler) ExportPuzzles(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := types.MustTenantIDFromContext(ctx)
	items, err := h.service.ExportPuzzles(ctx, tenantID, types.ChessPuzzleFilter{
		Theme: c.Query("theme"), Difficulty: c.Query("difficulty"),
	})
	if err != nil {
		chessFail(c, http.StatusInternalServerError, err)
		return
	}
	chessOK(c, items)
}

// ImportPuzzles POST /chess/puzzles/import {puzzles:[...]} — tạo mới; trả số đã thêm.
func (h *ChessLibraryHandler) ImportPuzzles(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := types.MustTenantIDFromContext(ctx)
	var b struct {
		Puzzles []types.ChessPuzzleBundle `json:"puzzles"`
	}
	if err := c.ShouldBindJSON(&b); err != nil {
		chessFail(c, http.StatusBadRequest, err)
		return
	}
	count, err := h.service.ImportPuzzles(ctx, tenantID, b.Puzzles)
	if err != nil {
		chessFail(c, http.StatusBadRequest, err)
		return
	}
	chessOK(c, gin.H{"imported": count})
}

// ListPuzzles GET /chess/puzzles?theme=&difficulty=
func (h *ChessLibraryHandler) ListPuzzles(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := types.MustTenantIDFromContext(ctx)
	puzzles, err := h.service.ListPuzzles(ctx, tenantID, types.ChessPuzzleFilter{
		Theme: c.Query("theme"), Difficulty: c.Query("difficulty"),
	})
	if err != nil {
		chessFail(c, http.StatusInternalServerError, err)
		return
	}
	chessOK(c, puzzles)
}

// RandomPuzzle GET /chess/puzzles/random?theme=&difficulty=
func (h *ChessLibraryHandler) RandomPuzzle(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := types.MustTenantIDFromContext(ctx)
	p, err := h.service.RandomPuzzle(ctx, tenantID, types.ChessPuzzleFilter{
		Theme: c.Query("theme"), Difficulty: c.Query("difficulty"),
	})
	if err != nil {
		chessFail(c, http.StatusNotFound, err)
		return
	}
	chessOK(c, p)
}

// GetPuzzle GET /chess/puzzles/:id
func (h *ChessLibraryHandler) GetPuzzle(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := types.MustTenantIDFromContext(ctx)
	p, err := h.service.GetPuzzle(ctx, tenantID, c.Param("id"))
	if err != nil {
		chessFail(c, http.StatusNotFound, err)
		return
	}
	chessOK(c, p)
}

// GetPuzzleBySlug GET /chess/puzzles/by-slug/:slug — giải mã wikilink [[puzzle/<slug>]].
func (h *ChessLibraryHandler) GetPuzzleBySlug(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := types.MustTenantIDFromContext(ctx)
	p, err := h.service.GetPuzzleBySlug(ctx, tenantID, c.Param("slug"))
	if err != nil {
		chessFail(c, http.StatusNotFound, err)
		return
	}
	chessOK(c, p)
}

// GetPuzzleBacklinks GET /chess/puzzles/by-slug/:slug/backlinks — trang wiki trỏ tới thế cờ.
func (h *ChessLibraryHandler) GetPuzzleBacklinks(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := types.MustTenantIDFromContext(ctx)
	links, err := h.service.GetPuzzleBacklinks(ctx, tenantID, c.Param("slug"))
	if err != nil {
		chessFail(c, http.StatusInternalServerError, err)
		return
	}
	chessOK(c, links)
}

type puzzleBody struct {
	Title      string `json:"title"`
	FEN        string `json:"fen"`
	Solution   string `json:"solution"`
	Theme      string `json:"theme"`
	Difficulty string `json:"difficulty"`
	Source     string `json:"source"`
}

// CreatePuzzle POST /chess/puzzles
func (h *ChessLibraryHandler) CreatePuzzle(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := types.MustTenantIDFromContext(ctx)
	var b puzzleBody
	if err := c.ShouldBindJSON(&b); err != nil {
		chessFail(c, http.StatusBadRequest, err)
		return
	}
	p, err := h.service.CreatePuzzle(ctx, &types.ChessPuzzle{
		TenantID: tenantID, Title: b.Title, FEN: b.FEN, Solution: b.Solution,
		Theme: b.Theme, Difficulty: b.Difficulty, Source: b.Source,
	})
	if err != nil {
		chessFail(c, http.StatusBadRequest, err)
		return
	}
	chessOK(c, p)
}

// UpdatePuzzle PUT /chess/puzzles/:id
func (h *ChessLibraryHandler) UpdatePuzzle(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := types.MustTenantIDFromContext(ctx)
	var b puzzleBody
	if err := c.ShouldBindJSON(&b); err != nil {
		chessFail(c, http.StatusBadRequest, err)
		return
	}
	p, err := h.service.UpdatePuzzle(ctx, &types.ChessPuzzle{
		ID: c.Param("id"), TenantID: tenantID, Title: b.Title, FEN: b.FEN,
		Solution: b.Solution, Theme: b.Theme, Difficulty: b.Difficulty, Source: b.Source,
	})
	if err != nil {
		chessFail(c, http.StatusBadRequest, err)
		return
	}
	chessOK(c, p)
}

// DeletePuzzle DELETE /chess/puzzles/:id
func (h *ChessLibraryHandler) DeletePuzzle(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := types.MustTenantIDFromContext(ctx)
	if err := h.service.DeletePuzzle(ctx, tenantID, c.Param("id")); err != nil {
		chessFail(c, http.StatusInternalServerError, err)
		return
	}
	chessOK(c, gin.H{"deleted": true})
}

// ---- Bảo trì KB ----

// ReindexKB POST /chess/library/reindex — đẩy lại toàn bộ ván+bài tập vào KB tri
// thức cờ. Dùng MỘT LẦN sau khi bật CHESS_KB_INDEX để index dữ liệu cũ (import
// hàng loạt không tự index). Trả lỗi rõ ràng nếu RAG cờ chưa bật hoặc KB cờ chưa
// có embedding model (fail-loud). Báo cáo trung thực: tổng / đã enqueue / lỗi.
func (h *ChessLibraryHandler) ReindexKB(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := types.MustTenantIDFromContext(ctx)
	res, err := h.service.ReindexAll(ctx, tenantID)
	if err != nil {
		chessFail(c, http.StatusBadRequest, err)
		return
	}
	chessOK(c, gin.H{
		"games_total":     res.GamesTotal,
		"puzzles_total":   res.PuzzlesTotal,
		"positions_total": res.PositionsTotal,
		"books_total":     res.BooksTotal,
		"chapters_total":  res.ChaptersTotal,
		"enqueued":        res.Enqueued,
		"failed":          res.Failed,
		"errors":          res.Errors,
		// Tương thích ngược với client/runbook cũ:
		"games_indexed":   res.GamesTotal,
		"puzzles_indexed": res.PuzzlesTotal,
		"note":            "đã enqueue để index; embedding chạy nền — kiểm tra GET /chess/library/index-status sau ~1 phút để xác nhận 'completed'",
	})
}

// IndexStatus GET /chess/library/index-status — báo trạng thái KB tri thức cờ để
// CHẨN ĐOÁN khi RAG rỗng (KB tồn tại?, có embedding model?, completed/pending/failed
// + mẫu lỗi). Luôn 200 (báo cáo trạng thái, không phải thao tác có thể thất bại).
func (h *ChessLibraryHandler) IndexStatus(c *gin.Context) {
	ctx := c.Request.Context()
	st, err := h.service.IndexStatus(ctx)
	if err != nil {
		chessFail(c, http.StatusInternalServerError, err)
		return
	}
	chessOK(c, st)
}
