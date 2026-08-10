package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/Tencent/WeKnora/internal/chess"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// chessLibraryService triển khai nghiệp vụ kho ván đấu & ngân hàng bài tập.
type chessLibraryService struct {
	repo         interfaces.ChessLibraryRepository
	chessRefRepo interfaces.WikiChessRefRepository
	aliasRepo    interfaces.ChessSlugAliasRepository
	indexer      *ChessKnowledgeIndexer
}

// NewChessLibraryService tạo service kho ván & bài tập cờ vua.
func NewChessLibraryService(
	repo interfaces.ChessLibraryRepository,
	chessRefRepo interfaces.WikiChessRefRepository,
	aliasRepo interfaces.ChessSlugAliasRepository,
	indexer *ChessKnowledgeIndexer,
) interfaces.ChessLibraryService {
	return &chessLibraryService{repo: repo, chessRefRepo: chessRefRepo, aliasRepo: aliasRepo, indexer: indexer}
}

// pruneChessRefs xóa các backlink wiki trỏ tới đối tượng cờ vừa bị xóa (best-effort).
func (s *chessLibraryService) pruneChessRefs(ctx context.Context, tenantID uint64, chessType, slug string) {
	if s.chessRefRepo == nil || slug == "" {
		return
	}
	_ = s.chessRefRepo.DeleteForChess(ctx, tenantID, chessType, slug)
}

// ---- Ván đấu ----

func (s *chessLibraryService) ListGames(ctx context.Context, tenantID uint64, f types.ChessGameFilter) ([]*types.ChessGame, error) {
	return s.repo.ListGames(ctx, tenantID, f)
}

func (s *chessLibraryService) GetGame(ctx context.Context, tenantID uint64, id string) (*types.ChessGame, error) {
	return s.repo.GetGame(ctx, tenantID, id)
}

func (s *chessLibraryService) GetGameBySlug(ctx context.Context, tenantID uint64, slug string) (*types.ChessGame, error) {
	g, err := s.repo.GetGameBySlug(ctx, tenantID, slug)
	if err == nil {
		return g, nil
	}
	// Không khớp chính xác → thử alias rồi fuzzy (giữ wikilink cũ/sai nhẹ sống).
	if resolved, ok := s.resolveAliasOrFuzzy(ctx, tenantID, types.ChessRefTypeGame, slug, s.repo.GameSlugs); ok {
		return s.repo.GetGameBySlug(ctx, tenantID, resolved)
	}
	return nil, err
}

// resolveAliasOrFuzzy nắn một slug "chết" về slug sống: ưu tiên alias (đổi tên/
// re-import), sau đó fuzzy trên pool slug do listSlugs cung cấp.
func (s *chessLibraryService) resolveAliasOrFuzzy(
	ctx context.Context, tenantID uint64, chessType, slug string,
	listSlugs func(context.Context, uint64) ([]string, error),
) (string, bool) {
	if s.aliasRepo != nil {
		if ns, ok, _ := s.aliasRepo.ResolveAlias(ctx, tenantID, chessType, slug); ok {
			return ns, true
		}
	}
	slugs, err := listSlugs(ctx, tenantID)
	if err != nil {
		return "", false
	}
	return resolveChessSlugFuzzy(slug, slugs)
}

func (s *chessLibraryService) GetGameBacklinks(ctx context.Context, tenantID uint64, slug string) ([]types.ChessBacklink, error) {
	if s.chessRefRepo == nil {
		return nil, nil
	}
	return s.chessRefRepo.ListBacklinks(ctx, tenantID, types.ChessRefTypeGame, slug)
}

func (s *chessLibraryService) CreateGame(ctx context.Context, game *types.ChessGame) (*types.ChessGame, error) {
	game.ID = uuid.New().String()
	slug, err := ensureUniqueChessSlug(ctx, game.TenantID, gameSlugBase(game), game.ID, s.repo.GameSlugExists)
	if err != nil {
		return nil, err
	}
	game.Slug = slug
	if err := s.repo.CreateGame(ctx, game); err != nil {
		return nil, err
	}
	if s.indexer != nil {
		_ = s.indexer.IndexGame(ctx, game) // best-effort: không chặn CRUD
	}
	return game, nil
}

func (s *chessLibraryService) UpdateGame(ctx context.Context, game *types.ChessGame) (*types.ChessGame, error) {
	if _, err := s.repo.GetGame(ctx, game.TenantID, game.ID); err != nil {
		return nil, err
	}
	if err := s.repo.UpdateGame(ctx, game); err != nil {
		return nil, err
	}
	updated, err := s.repo.GetGame(ctx, game.TenantID, game.ID)
	if err == nil && updated != nil && s.indexer != nil {
		_ = s.indexer.IndexGame(ctx, updated) // best-effort: không chặn CRUD
	}
	return updated, err
}

// RenameGameSlug đổi slug ván sang newSlug (chuẩn hóa + đảm bảo duy nhất theo
// tenant) và ghi alias slug-cũ→mới để wikilink [[game/<cũ>]] vẫn giải mã đúng.
func (s *chessLibraryService) RenameGameSlug(ctx context.Context, tenantID uint64, id, newSlug string) (*types.ChessGame, error) {
	g, err := s.repo.GetGame(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	base := slugifyChess(newSlug)
	if base == "" {
		return nil, fmt.Errorf("slug mới không hợp lệ (cần chứa chữ/số)")
	}
	if base == g.Slug {
		return g, nil // không đổi
	}
	unique, err := ensureUniqueChessSlug(ctx, tenantID, base, g.ID, s.repo.GameSlugExists)
	if err != nil {
		return nil, err
	}
	oldSlug := g.Slug
	if err := s.repo.UpdateGameSlug(ctx, tenantID, id, unique); err != nil {
		return nil, err
	}
	if s.aliasRepo != nil && oldSlug != "" {
		_ = s.aliasRepo.AddAlias(ctx, tenantID, types.ChessRefTypeGame, oldSlug, unique)
	}
	updated, err := s.repo.GetGame(ctx, tenantID, id)
	if err == nil && updated != nil && s.indexer != nil {
		_ = s.indexer.IndexGame(ctx, updated) // best-effort: không chặn CRUD
	}
	return updated, err
}

func (s *chessLibraryService) DeleteGame(ctx context.Context, tenantID uint64, id string) error {
	g, _ := s.repo.GetGame(ctx, tenantID, id)
	if err := s.repo.DeleteGame(ctx, tenantID, id); err != nil {
		return err
	}
	if g != nil {
		s.pruneChessRefs(ctx, tenantID, types.ChessRefTypeGame, g.Slug)
		if s.indexer != nil {
			s.indexer.Remove(ctx, tenantID, types.ChessRefTypeGame, g.Slug)
		}
	}
	return nil
}

// ImportGames tách PGN nhiều ván → tạo nhiều ChessGame, trả số ván đã thêm.
func (s *chessLibraryService) ImportGames(ctx context.Context, tenantID uint64, pgn string) (int, error) {
	if strings.TrimSpace(pgn) == "" {
		return 0, fmt.Errorf("PGN rỗng")
	}
	imported, err := chess.ParseMultiPGN(pgn)
	if err != nil {
		return 0, err
	}
	if len(imported) == 0 {
		return 0, fmt.Errorf("không tìm thấy ván cờ nào trong PGN")
	}
	games := make([]*types.ChessGame, 0, len(imported))
	for _, ig := range imported {
		id := uuid.New().String()
		games = append(games, &types.ChessGame{
			ID:       id,
			TenantID: tenantID,
			// Import hàng loạt: gán slug xác định "g-<id8>" để tránh N lần dò
			// trùng. Có thể humanize sau bằng backfill; link vẫn hoạt động.
			Slug:     "g-" + id8(id),
			White:    ig.TagOr("White", "?"),
			Black:    ig.TagOr("Black", "?"),
			Result:   ig.TagOr("Result", ig.Outcome),
			ECO:      ig.TagOr("ECO", ""),
			Event:    ig.TagOr("Event", ""),
			Date:     ig.TagOr("Date", ""),
			PGN:      ig.PGN,
			PlyCount: ig.PlyCount,
		})
	}
	if err := s.repo.CreateGames(ctx, games); err != nil {
		return 0, err
	}
	return len(games), nil
}

// ---- Bài tập ----

func (s *chessLibraryService) ListPuzzles(ctx context.Context, tenantID uint64, f types.ChessPuzzleFilter) ([]*types.ChessPuzzle, error) {
	return s.repo.ListPuzzles(ctx, tenantID, f)
}

func (s *chessLibraryService) GetPuzzle(ctx context.Context, tenantID uint64, id string) (*types.ChessPuzzle, error) {
	return s.repo.GetPuzzle(ctx, tenantID, id)
}

func (s *chessLibraryService) GetPuzzleBySlug(ctx context.Context, tenantID uint64, slug string) (*types.ChessPuzzle, error) {
	p, err := s.repo.GetPuzzleBySlug(ctx, tenantID, slug)
	if err == nil {
		return p, nil
	}
	if resolved, ok := s.resolveAliasOrFuzzy(ctx, tenantID, types.ChessRefTypePuzzle, slug, s.repo.PuzzleSlugs); ok {
		return s.repo.GetPuzzleBySlug(ctx, tenantID, resolved)
	}
	return nil, err
}

func (s *chessLibraryService) GetPuzzleBacklinks(ctx context.Context, tenantID uint64, slug string) ([]types.ChessBacklink, error) {
	if s.chessRefRepo == nil {
		return nil, nil
	}
	return s.chessRefRepo.ListBacklinks(ctx, tenantID, types.ChessRefTypePuzzle, slug)
}

func (s *chessLibraryService) CreatePuzzle(ctx context.Context, puzzle *types.ChessPuzzle) (*types.ChessPuzzle, error) {
	if strings.TrimSpace(puzzle.FEN) == "" {
		return nil, fmt.Errorf("thiếu thế cờ FEN")
	}
	if err := chess.ValidateFEN(puzzle.FEN); err != nil {
		return nil, fmt.Errorf("FEN không hợp lệ: %v", err)
	}
	puzzle.ID = uuid.New().String()
	slug, err := ensureUniqueChessSlug(ctx, puzzle.TenantID, puzzleSlugBase(puzzle), puzzle.ID, s.repo.PuzzleSlugExists)
	if err != nil {
		return nil, err
	}
	puzzle.Slug = slug
	if err := s.repo.CreatePuzzle(ctx, puzzle); err != nil {
		return nil, err
	}
	if s.indexer != nil {
		_ = s.indexer.IndexPuzzle(ctx, puzzle) // best-effort: không chặn CRUD
	}
	return puzzle, nil
}

func (s *chessLibraryService) UpdatePuzzle(ctx context.Context, puzzle *types.ChessPuzzle) (*types.ChessPuzzle, error) {
	if _, err := s.repo.GetPuzzle(ctx, puzzle.TenantID, puzzle.ID); err != nil {
		return nil, err
	}
	if puzzle.FEN != "" {
		if err := chess.ValidateFEN(puzzle.FEN); err != nil {
			return nil, fmt.Errorf("FEN không hợp lệ: %v", err)
		}
	}
	if err := s.repo.UpdatePuzzle(ctx, puzzle); err != nil {
		return nil, err
	}
	updated, err := s.repo.GetPuzzle(ctx, puzzle.TenantID, puzzle.ID)
	if err == nil && updated != nil && s.indexer != nil {
		_ = s.indexer.IndexPuzzle(ctx, updated) // best-effort: không chặn CRUD
	}
	return updated, err
}

// RenamePuzzleSlug đổi slug bài tập sang newSlug + ghi alias slug-cũ→mới.
func (s *chessLibraryService) RenamePuzzleSlug(ctx context.Context, tenantID uint64, id, newSlug string) (*types.ChessPuzzle, error) {
	p, err := s.repo.GetPuzzle(ctx, tenantID, id)
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
	unique, err := ensureUniqueChessSlug(ctx, tenantID, base, p.ID, s.repo.PuzzleSlugExists)
	if err != nil {
		return nil, err
	}
	oldSlug := p.Slug
	if err := s.repo.UpdatePuzzleSlug(ctx, tenantID, id, unique); err != nil {
		return nil, err
	}
	if s.aliasRepo != nil && oldSlug != "" {
		_ = s.aliasRepo.AddAlias(ctx, tenantID, types.ChessRefTypePuzzle, oldSlug, unique)
	}
	updated, err := s.repo.GetPuzzle(ctx, tenantID, id)
	if err == nil && updated != nil && s.indexer != nil {
		_ = s.indexer.IndexPuzzle(ctx, updated) // best-effort: không chặn CRUD
	}
	return updated, err
}

func (s *chessLibraryService) DeletePuzzle(ctx context.Context, tenantID uint64, id string) error {
	p, _ := s.repo.GetPuzzle(ctx, tenantID, id)
	if err := s.repo.DeletePuzzle(ctx, tenantID, id); err != nil {
		return err
	}
	if p != nil {
		s.pruneChessRefs(ctx, tenantID, types.ChessRefTypePuzzle, p.Slug)
		if s.indexer != nil {
			s.indexer.Remove(ctx, tenantID, types.ChessRefTypePuzzle, p.Slug)
		}
	}
	return nil
}

func (s *chessLibraryService) RandomPuzzle(ctx context.Context, tenantID uint64, f types.ChessPuzzleFilter) (*types.ChessPuzzle, error) {
	return s.repo.RandomPuzzle(ctx, tenantID, f)
}

// ---- Export / Import (sao lưu & chia sẻ) ----

// ExportGamesPGN xuất các ván (theo filter) thành một chuỗi PGN nhiều ván.
func (s *chessLibraryService) ExportGamesPGN(ctx context.Context, tenantID uint64, f types.ChessGameFilter) (string, error) {
	games, err := s.repo.ListGames(ctx, tenantID, f)
	if err != nil {
		return "", err
	}
	parts := make([]string, 0, len(games))
	for _, g := range games {
		if strings.TrimSpace(g.PGN) == "" {
			continue
		}
		parts = append(parts, strings.TrimSpace(g.PGN))
	}
	return strings.Join(parts, "\n\n"), nil
}

// ExportPuzzles xuất các bài tập (theo filter) để sao lưu/chia sẻ.
func (s *chessLibraryService) ExportPuzzles(ctx context.Context, tenantID uint64, f types.ChessPuzzleFilter) ([]types.ChessPuzzleBundle, error) {
	puzzles, err := s.repo.ListPuzzles(ctx, tenantID, f)
	if err != nil {
		return nil, err
	}
	out := make([]types.ChessPuzzleBundle, 0, len(puzzles))
	for _, p := range puzzles {
		out = append(out, types.ChessPuzzleBundle{
			Title: p.Title, FEN: p.FEN, Solution: p.Solution,
			Theme: p.Theme, Difficulty: p.Difficulty, Source: p.Source,
		})
	}
	return out, nil
}

// ImportPuzzles nhập danh sách bài tập, LUÔN tạo mới trong tenant hiện tại (tái dùng
// CreatePuzzle để validate FEN + sinh ID/slug). Trả số bài đã thêm; bỏ qua bài FEN
// lỗi để không chặn cả lô.
func (s *chessLibraryService) ImportPuzzles(ctx context.Context, tenantID uint64, items []types.ChessPuzzleBundle) (int, error) {
	created := 0
	for i := range items {
		it := items[i]
		if strings.TrimSpace(it.FEN) == "" {
			continue
		}
		_, err := s.CreatePuzzle(ctx, &types.ChessPuzzle{
			TenantID: tenantID, Title: it.Title, FEN: it.FEN, Solution: it.Solution,
			Theme: it.Theme, Difficulty: it.Difficulty, Source: it.Source,
		})
		if err != nil {
			continue
		}
		created++
	}
	if created == 0 && len(items) > 0 {
		return 0, fmt.Errorf("không nhập được bài tập nào (kiểm tra FEN/định dạng JSON)")
	}
	return created, nil
}

// ReindexAll đẩy lại toàn bộ ván + bài tập của tenant vào KB tri thức cờ. Dùng sau
// khi bật CHESS_KB_INDEX để index dữ liệu cũ (import hàng loạt KHÔNG tự index).
//
// FAIL-LOUD: trước khi đẩy, kiểm tra KB cờ có embedding model — nếu KHÔNG thì trả
// lỗi rõ ràng (không "success" giả) vì RAG sẽ không truy hồi được nội dung. Báo cáo
// TRUNG THỰC: tách tổng vs số đã enqueue vs lỗi. Lưu ý "enqueued" ≠ "đã embed" —
// embedding chạy nền, kiểm tra hoàn tất qua IndexStatus (completed).
func (s *chessLibraryService) ReindexAll(ctx context.Context, tenantID uint64) (*types.ChessReindexResult, error) {
	if s.indexer == nil || !s.indexer.Enabled() {
		return nil, fmt.Errorf("CHESS_KB_INDEX chưa bật — không có gì để index")
	}
	// Tiền điều kiện: KB cờ phải có embedding model, nếu không index vô nghĩa.
	kb, err := s.indexer.ensureChessKB(ctx)
	if err != nil || kb == nil {
		if err == nil {
			err = fmt.Errorf("KB cờ không khả dụng")
		}
		return nil, fmt.Errorf("không sẵn sàng index: %w", err)
	}
	if strings.TrimSpace(kb.EmbeddingModelID) == "" {
		return nil, fmt.Errorf(
			"KB %q chưa có embedding model — RAG sẽ không truy hồi được nội dung; "+
				"hãy cấu hình embedding cho ít nhất 1 KB rồi thử lại", chessKBName)
	}

	res := &types.ChessReindexResult{}
	record := func(kind, slug string, e error) {
		if e != nil {
			res.Failed++
			if len(res.Errors) < 5 {
				res.Errors = append(res.Errors, fmt.Sprintf("%s/%s: %v", kind, slug, e))
			}
			return
		}
		res.Enqueued++
	}

	games, err := s.repo.ListGames(ctx, tenantID, types.ChessGameFilter{})
	if err != nil {
		return nil, err
	}
	res.GamesTotal = len(games)
	for _, g := range games {
		record("game", g.Slug, s.indexer.IndexGame(ctx, g))
	}

	puzzles, err := s.repo.ListPuzzles(ctx, tenantID, types.ChessPuzzleFilter{})
	if err != nil {
		return res, err
	}
	res.PuzzlesTotal = len(puzzles)
	for _, p := range puzzles {
		record("puzzle", p.Slug, s.indexer.IndexPuzzle(ctx, p))
	}

	positions, err := s.repo.ListPositions(ctx, tenantID, types.ChessPositionFilter{})
	if err != nil {
		return res, err
	}
	res.PositionsTotal = len(positions)
	for _, p := range positions {
		record("position", p.Slug, s.indexer.IndexPosition(ctx, p))
	}
	return res, nil
}

// IndexStatus báo cáo trạng thái KB tri thức cờ để chẩn đoán RAG (ủy quyền indexer).
func (s *chessLibraryService) IndexStatus(ctx context.Context) (*types.ChessIndexStatus, error) {
	if s.indexer == nil {
		return &types.ChessIndexStatus{}, nil
	}
	return s.indexer.IndexStatus(ctx)
}
