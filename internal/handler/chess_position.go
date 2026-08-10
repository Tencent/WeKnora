package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/types"
)

// chess_position.go bổ sung API "Ngân hàng thế cờ" vào CÙNG ChessLibraryHandler
// (khai báo ở chess_library.go) — position là "ngăn" thứ ba cạnh games/puzzles,
// không phải handler riêng, để router.go chỉ cần mở rộng RegisterChessLibraryRoutes
// đã có thay vì thêm hàm Register* mới.

// ListPositions GET /chess/positions?category=&level=&eco=&q=&source_game_id=
func (h *ChessLibraryHandler) ListPositions(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := types.MustTenantIDFromContext(ctx)
	positions, err := h.service.ListPositions(ctx, tenantID, types.ChessPositionFilter{
		Category: c.Query("category"), Level: c.Query("level"), ECO: c.Query("eco"),
		SourceGameID: c.Query("source_game_id"), Keyword: c.Query("q"),
	})
	if err != nil {
		chessFail(c, http.StatusInternalServerError, err)
		return
	}
	chessOK(c, positions)
}

// GetPosition GET /chess/positions/:id
func (h *ChessLibraryHandler) GetPosition(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := types.MustTenantIDFromContext(ctx)
	p, err := h.service.GetPosition(ctx, tenantID, c.Param("id"))
	if err != nil {
		chessFail(c, http.StatusNotFound, err)
		return
	}
	chessOK(c, p)
}

// GetPositionBySlug GET /chess/positions/by-slug/:slug — giải mã wikilink [[position/<slug>]].
func (h *ChessLibraryHandler) GetPositionBySlug(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := types.MustTenantIDFromContext(ctx)
	p, err := h.service.GetPositionBySlug(ctx, tenantID, c.Param("slug"))
	if err != nil {
		chessFail(c, http.StatusNotFound, err)
		return
	}
	chessOK(c, p)
}

// GetPositionBacklinks GET /chess/positions/by-slug/:slug/backlinks — trang wiki/
// bài giảng/thế cờ khác trỏ tới thế cờ này.
func (h *ChessLibraryHandler) GetPositionBacklinks(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := types.MustTenantIDFromContext(ctx)
	links, err := h.service.GetPositionBacklinks(ctx, tenantID, c.Param("slug"))
	if err != nil {
		chessFail(c, http.StatusInternalServerError, err)
		return
	}
	chessOK(c, links)
}

type positionBody struct {
	Title        string `json:"title"`
	FEN          string `json:"fen"`
	Category     string `json:"category"`
	Level        string `json:"level"`
	ECO          string `json:"eco"`
	Orientation  string `json:"orientation"`
	Assessment   string `json:"assessment"`
	Annotation   string `json:"annotation"`
	Tags         string `json:"tags"`
	Source       string `json:"source"`
	SourceGameID string `json:"source_game_id"`
	SourcePly    int    `json:"source_ply"`
}

// CreatePosition POST /chess/positions
func (h *ChessLibraryHandler) CreatePosition(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := types.MustTenantIDFromContext(ctx)
	var b positionBody
	if err := c.ShouldBindJSON(&b); err != nil {
		chessFail(c, http.StatusBadRequest, err)
		return
	}
	p, err := h.service.CreatePosition(ctx, &types.ChessPosition{
		TenantID: tenantID, Title: b.Title, FEN: b.FEN, Category: b.Category, Level: b.Level,
		ECO: b.ECO, Orientation: b.Orientation, Assessment: b.Assessment, Annotation: b.Annotation,
		Tags: b.Tags, Source: b.Source, SourceGameID: b.SourceGameID, SourcePly: b.SourcePly,
	})
	if err != nil {
		chessFail(c, http.StatusBadRequest, err)
		return
	}
	chessOK(c, p)
}

// UpdatePosition PUT /chess/positions/:id
func (h *ChessLibraryHandler) UpdatePosition(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := types.MustTenantIDFromContext(ctx)
	var b positionBody
	if err := c.ShouldBindJSON(&b); err != nil {
		chessFail(c, http.StatusBadRequest, err)
		return
	}
	p, err := h.service.UpdatePosition(ctx, &types.ChessPosition{
		ID: c.Param("id"), TenantID: tenantID, Title: b.Title, FEN: b.FEN,
		Category: b.Category, Level: b.Level, ECO: b.ECO, Orientation: b.Orientation,
		Assessment: b.Assessment, Annotation: b.Annotation, Tags: b.Tags, Source: b.Source,
		SourceGameID: b.SourceGameID, SourcePly: b.SourcePly,
	})
	if err != nil {
		chessFail(c, http.StatusBadRequest, err)
		return
	}
	chessOK(c, p)
}

// RenamePositionSlug PUT /chess/positions/:id/slug {slug} — đổi slug thế cờ, giữ link cũ.
func (h *ChessLibraryHandler) RenamePositionSlug(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := types.MustTenantIDFromContext(ctx)
	var b slugBody
	if err := c.ShouldBindJSON(&b); err != nil {
		chessFail(c, http.StatusBadRequest, err)
		return
	}
	p, err := h.service.RenamePositionSlug(ctx, tenantID, c.Param("id"), b.Slug)
	if err != nil {
		chessFail(c, http.StatusBadRequest, err)
		return
	}
	chessOK(c, p)
}

// DeletePosition DELETE /chess/positions/:id
func (h *ChessLibraryHandler) DeletePosition(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := types.MustTenantIDFromContext(ctx)
	if err := h.service.DeletePosition(ctx, tenantID, c.Param("id")); err != nil {
		chessFail(c, http.StatusInternalServerError, err)
		return
	}
	chessOK(c, gin.H{"deleted": true})
}

// ExportPositions GET /chess/positions/export?category=&level=&eco= — danh sách thế cờ (JSON).
func (h *ChessLibraryHandler) ExportPositions(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := types.MustTenantIDFromContext(ctx)
	items, err := h.service.ExportPositions(ctx, tenantID, types.ChessPositionFilter{
		Category: c.Query("category"), Level: c.Query("level"), ECO: c.Query("eco"),
	})
	if err != nil {
		chessFail(c, http.StatusInternalServerError, err)
		return
	}
	chessOK(c, items)
}

// ImportPositions POST /chess/positions/import {positions:[...]} — tạo mới; trả số đã thêm.
func (h *ChessLibraryHandler) ImportPositions(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := types.MustTenantIDFromContext(ctx)
	var b struct {
		Positions []types.ChessPositionBundle `json:"positions"`
	}
	if err := c.ShouldBindJSON(&b); err != nil {
		chessFail(c, http.StatusBadRequest, err)
		return
	}
	count, err := h.service.ImportPositions(ctx, tenantID, b.Positions)
	if err != nil {
		chessFail(c, http.StatusBadRequest, err)
		return
	}
	chessOK(c, gin.H{"imported": count})
}
