package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/Tencent/WeKnora/internal/chess"
	"github.com/Tencent/WeKnora/internal/types"
)

// chess_library_position.go bổ sung nghiệp vụ cho "Ngân hàng thế cờ"
// (chess_positions) vào CÙNG struct chessLibraryService (khai báo ở
// chess_library.go) — position là "ngăn" thứ ba cạnh games/puzzles, không phải
// service riêng, để container.go/router.go không phải mở rộng.
//
// LƯU Ý QUAN TRỌNG: KHÔNG được validate FEN bằng chess.ValidateFEN/notnil trực
// tiếp theo kiểu "đủ luật cờ" — ngân hàng thế cờ CỐ Ý cho phép thế cờ giản lược
// không có quân Vua (dạy trẻ mới học). chess.NormalizeFEN bù trường thiếu rồi
// chỉ kiểm cấu trúc (đã xác nhận: notnil.decodeFEN không đòi hỏi sự tồn tại của
// Vua). Không gọi chess.FENAfterMove/UCIToSAN/gameFromFEN cho position — những
// hàm đó dựng notnil.Game và có thể vỡ với thế cờ thiếu Vua.

// ---- Thế cờ ----

func (s *chessLibraryService) ListPositions(ctx context.Context, tenantID uint64, f types.ChessPositionFilter) ([]*types.ChessPosition, error) {
	return s.repo.ListPositions(ctx, tenantID, f)
}

func (s *chessLibraryService) GetPosition(ctx context.Context, tenantID uint64, id string) (*types.ChessPosition, error) {
	return s.repo.GetPosition(ctx, tenantID, id)
}

func (s *chessLibraryService) GetPositionBySlug(ctx context.Context, tenantID uint64, slug string) (*types.ChessPosition, error) {
	p, err := s.repo.GetPositionBySlug(ctx, tenantID, slug)
	if err == nil {
		return p, nil
	}
	if resolved, ok := s.resolveAliasOrFuzzy(ctx, tenantID, types.ChessRefTypePosition, slug, s.repo.PositionSlugs); ok {
		return s.repo.GetPositionBySlug(ctx, tenantID, resolved)
	}
	return nil, err
}

func (s *chessLibraryService) GetPositionBacklinks(ctx context.Context, tenantID uint64, slug string) ([]types.ChessBacklink, error) {
	if s.chessRefRepo == nil {
		return nil, nil
	}
	return s.chessRefRepo.ListBacklinks(ctx, tenantID, types.ChessRefTypePosition, slug)
}

func (s *chessLibraryService) CreatePosition(ctx context.Context, position *types.ChessPosition) (*types.ChessPosition, error) {
	if strings.TrimSpace(position.FEN) == "" {
		return nil, fmt.Errorf("thiếu thế cờ FEN")
	}
	normalized, err := chess.NormalizeFEN(position.FEN)
	if err != nil {
		return nil, fmt.Errorf("FEN không hợp lệ: %v", err)
	}
	position.FEN = normalized
	position.FENKey = chess.FENKey(normalized)
	if position.SideToMove == "" {
		position.SideToMove = chess.SideToMove(normalized)
	}
	if position.Orientation == "" {
		position.Orientation = position.SideToMove
	}
	position.ID = uuid.New().String()
	slug, err := ensureUniqueChessSlug(ctx, position.TenantID, positionSlugBase(position), position.ID, s.repo.PositionSlugExists)
	if err != nil {
		return nil, err
	}
	position.Slug = slug
	if err := s.repo.CreatePosition(ctx, position); err != nil {
		return nil, err
	}
	s.syncPositionChessRefs(ctx, position)
	if s.indexer != nil {
		_ = s.indexer.IndexPosition(ctx, position) // best-effort: không chặn CRUD
	}
	return position, nil
}

func (s *chessLibraryService) UpdatePosition(ctx context.Context, position *types.ChessPosition) (*types.ChessPosition, error) {
	if _, err := s.repo.GetPosition(ctx, position.TenantID, position.ID); err != nil {
		return nil, err
	}
	if position.FEN != "" {
		normalized, err := chess.NormalizeFEN(position.FEN)
		if err != nil {
			return nil, fmt.Errorf("FEN không hợp lệ: %v", err)
		}
		position.FEN = normalized
		position.FENKey = chess.FENKey(normalized)
		if position.SideToMove == "" {
			position.SideToMove = chess.SideToMove(normalized)
		}
	}
	if err := s.repo.UpdatePosition(ctx, position); err != nil {
		return nil, err
	}
	updated, err := s.repo.GetPosition(ctx, position.TenantID, position.ID)
	if err != nil || updated == nil {
		return updated, err
	}
	s.syncPositionChessRefs(ctx, updated)
	if s.indexer != nil {
		_ = s.indexer.IndexPosition(ctx, updated) // best-effort: không chặn CRUD
	}
	return updated, nil
}

// RenamePositionSlug đổi slug thế cờ sang newSlug + ghi alias slug-cũ→mới để
// wikilink [[position/<cũ>]] vẫn giải mã đúng.
func (s *chessLibraryService) RenamePositionSlug(ctx context.Context, tenantID uint64, id, newSlug string) (*types.ChessPosition, error) {
	p, err := s.repo.GetPosition(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	base := slugifyChess(newSlug)
	if base == "" {
		return nil, fmt.Errorf("slug mới không hợp lệ (cần chứa chữ/số)")
	}
	if base == p.Slug {
		return p, nil
	}
	unique, err := ensureUniqueChessSlug(ctx, tenantID, base, p.ID, s.repo.PositionSlugExists)
	if err != nil {
		return nil, err
	}
	oldSlug := p.Slug
	if err := s.repo.UpdatePositionSlug(ctx, tenantID, id, unique); err != nil {
		return nil, err
	}
	if s.aliasRepo != nil && oldSlug != "" {
		_ = s.aliasRepo.AddAlias(ctx, tenantID, types.ChessRefTypePosition, oldSlug, unique)
	}
	updated, err := s.repo.GetPosition(ctx, tenantID, id)
	if err != nil || updated == nil {
		return updated, err
	}
	// Chú giải của thế cờ có thể tự tham chiếu slug cũ trong một số ngữ cảnh
	// hiển thị — đồng bộ lại nguồn ref dưới slug MỚI để backlink nhất quán.
	s.syncPositionChessRefs(ctx, updated)
	if s.indexer != nil {
		_ = s.indexer.IndexPosition(ctx, updated) // best-effort: không chặn CRUD
	}
	return updated, nil
}

func (s *chessLibraryService) DeletePosition(ctx context.Context, tenantID uint64, id string) error {
	p, _ := s.repo.GetPosition(ctx, tenantID, id)
	if err := s.repo.DeletePosition(ctx, tenantID, id); err != nil {
		return err
	}
	if p != nil {
		// Xóa cả ref ĐÍCH tới thế cờ này (backlink từ nơi khác) lẫn ref NGUỒN từ
		// chú giải của chính nó (nó từng trỏ đi đâu).
		s.pruneChessRefs(ctx, tenantID, types.ChessRefTypePosition, p.Slug)
		if s.chessRefRepo != nil && p.Slug != "" {
			_ = s.chessRefRepo.DeleteForPosition(ctx, tenantID, p.Slug)
		}
		if s.indexer != nil {
			s.indexer.Remove(ctx, tenantID, types.ChessRefTypePosition, p.Slug)
		}
	}
	return nil
}

// ListPositionsByGame liệt kê các thế cờ đã trích từ MỘT ván cụ thể (panel
// "Thế cờ đã trích" trong Kho ván).
func (s *chessLibraryService) ListPositionsByGame(ctx context.Context, tenantID uint64, gameID string) ([]*types.ChessPosition, error) {
	return s.repo.ListPositionsByGame(ctx, tenantID, gameID)
}

// syncPositionChessRefs đồng bộ wiki_chess_refs theo chú giải (annotation) của
// một thế cờ (nguồn position) — TÁI DÙNG parseChessRefSlugs (wiki_page.go, cùng
// package) như bài giảng, để chú giải trở thành nguồn phát wikilink.
func (s *chessLibraryService) syncPositionChessRefs(ctx context.Context, p *types.ChessPosition) {
	if s.chessRefRepo == nil || p == nil || p.Slug == "" {
		return
	}
	refs := parseChessRefSlugs(p.Annotation)
	for i := range refs {
		refs[i].ID = uuid.New().String()
		refs[i].TenantID = p.TenantID
		refs[i].SourceType = types.ChessRefSourcePosition
		refs[i].PageSlug = p.Slug
	}
	_ = s.chessRefRepo.ReplaceForPosition(ctx, p.TenantID, p.Slug, refs)
}

// ---- Export / Import ----

// ExportPositions xuất các thế cờ (theo filter) để sao lưu/chia sẻ.
func (s *chessLibraryService) ExportPositions(ctx context.Context, tenantID uint64, f types.ChessPositionFilter) ([]types.ChessPositionBundle, error) {
	positions, err := s.repo.ListPositions(ctx, tenantID, f)
	if err != nil {
		return nil, err
	}
	out := make([]types.ChessPositionBundle, 0, len(positions))
	for _, p := range positions {
		out = append(out, types.ChessPositionBundle{
			Title: p.Title, FEN: p.FEN, Category: p.Category, Level: p.Level, ECO: p.ECO,
			Orientation: p.Orientation, Assessment: p.Assessment, Annotation: p.Annotation,
			Tags: p.Tags, Source: p.Source,
		})
	}
	return out, nil
}

// ImportPositions nhập danh sách thế cờ, LUÔN tạo mới trong tenant hiện tại
// (tái dùng CreatePosition để chuẩn hóa FEN + sinh ID/slug). Trả số thế cờ đã
// thêm; bỏ qua bản FEN lỗi để không chặn cả lô.
func (s *chessLibraryService) ImportPositions(ctx context.Context, tenantID uint64, items []types.ChessPositionBundle) (int, error) {
	created := 0
	for i := range items {
		it := items[i]
		if strings.TrimSpace(it.FEN) == "" {
			continue
		}
		_, err := s.CreatePosition(ctx, &types.ChessPosition{
			TenantID: tenantID, Title: it.Title, FEN: it.FEN, Category: it.Category, Level: it.Level,
			ECO: it.ECO, Orientation: it.Orientation, Assessment: it.Assessment,
			Annotation: it.Annotation, Tags: it.Tags, Source: it.Source,
		})
		if err != nil {
			continue
		}
		created++
	}
	if created == 0 && len(items) > 0 {
		return 0, fmt.Errorf("không nhập được thế cờ nào (kiểm tra FEN/định dạng JSON)")
	}
	return created, nil
}
