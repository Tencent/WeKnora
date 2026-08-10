package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// --- Fake gọn: embed interface để chỉ override method position/alias cần dùng,
// cùng kỹ thuật với chess_knowledge_indexer_test.go (stubKBService...). ---

type fakePositionRepo struct {
	interfaces.ChessLibraryRepository
	byID map[string]*types.ChessPosition
}

func newFakePositionRepo() *fakePositionRepo {
	return &fakePositionRepo{byID: map[string]*types.ChessPosition{}}
}

func (r *fakePositionRepo) ListPositions(ctx context.Context, tenantID uint64, f types.ChessPositionFilter) ([]*types.ChessPosition, error) {
	out := make([]*types.ChessPosition, 0, len(r.byID))
	for _, p := range r.byID {
		out = append(out, p)
	}
	return out, nil
}

func (r *fakePositionRepo) GetPosition(ctx context.Context, tenantID uint64, id string) (*types.ChessPosition, error) {
	p, ok := r.byID[id]
	if !ok {
		return nil, fmt.Errorf("position not found: %s", id)
	}
	return p, nil
}

func (r *fakePositionRepo) GetPositionBySlug(ctx context.Context, tenantID uint64, slug string) (*types.ChessPosition, error) {
	for _, p := range r.byID {
		if p.Slug == slug {
			return p, nil
		}
	}
	return nil, fmt.Errorf("position not found for slug: %s", slug)
}

func (r *fakePositionRepo) PositionSlugs(ctx context.Context, tenantID uint64) ([]string, error) {
	out := make([]string, 0, len(r.byID))
	for _, p := range r.byID {
		if p.Slug != "" {
			out = append(out, p.Slug)
		}
	}
	return out, nil
}

func (r *fakePositionRepo) PositionSlugExists(ctx context.Context, tenantID uint64, slug string) (bool, error) {
	for _, p := range r.byID {
		if p.Slug == slug {
			return true, nil
		}
	}
	return false, nil
}

func (r *fakePositionRepo) CreatePosition(ctx context.Context, p *types.ChessPosition) error {
	cp := *p
	r.byID[p.ID] = &cp
	return nil
}

func (r *fakePositionRepo) UpdatePosition(ctx context.Context, p *types.ChessPosition) error {
	existing, ok := r.byID[p.ID]
	if !ok {
		return fmt.Errorf("position not found: %s", p.ID)
	}
	cp := *p
	cp.Slug = existing.Slug // UpdatePosition cố ý không đụng slug, như UpdateGame/UpdatePuzzle
	r.byID[p.ID] = &cp
	return nil
}

func (r *fakePositionRepo) UpdatePositionSlug(ctx context.Context, tenantID uint64, id, slug string) error {
	p, ok := r.byID[id]
	if !ok {
		return fmt.Errorf("position not found: %s", id)
	}
	p.Slug = slug
	return nil
}

func (r *fakePositionRepo) DeletePosition(ctx context.Context, tenantID uint64, id string) error {
	delete(r.byID, id)
	return nil
}

func (r *fakePositionRepo) ListPositionsByGame(ctx context.Context, tenantID uint64, gameID string) ([]*types.ChessPosition, error) {
	var out []*types.ChessPosition
	for _, p := range r.byID {
		if p.SourceGameID == gameID {
			out = append(out, p)
		}
	}
	return out, nil
}

func (r *fakePositionRepo) FindByFENKey(ctx context.Context, tenantID uint64, fenKey string) ([]*types.ChessPosition, error) {
	var out []*types.ChessPosition
	for _, p := range r.byID {
		if p.FENKey == fenKey {
			out = append(out, p)
		}
	}
	return out, nil
}

type fakePositionChessRefRepo struct {
	interfaces.WikiChessRefRepository
}

func (fakePositionChessRefRepo) ReplaceForPosition(ctx context.Context, tenantID uint64, positionSlug string, refs []types.WikiChessRef) error {
	return nil
}
func (fakePositionChessRefRepo) DeleteForPosition(ctx context.Context, tenantID uint64, positionSlug string) error {
	return nil
}
func (fakePositionChessRefRepo) DeleteForChess(ctx context.Context, tenantID uint64, chessType, chessSlug string) error {
	return nil
}
func (fakePositionChessRefRepo) ListBacklinks(ctx context.Context, tenantID uint64, chessType, chessSlug string) ([]types.ChessBacklink, error) {
	return nil, nil
}

type fakePositionAliasRepo struct {
	interfaces.ChessSlugAliasRepository
	aliases map[string]string // key: chessType+"/"+oldSlug -> newSlug
}

func (r *fakePositionAliasRepo) ResolveAlias(ctx context.Context, tenantID uint64, chessType, oldSlug string) (string, bool, error) {
	ns, ok := r.aliases[chessType+"/"+oldSlug]
	return ns, ok, nil
}

func (r *fakePositionAliasRepo) AddAlias(ctx context.Context, tenantID uint64, chessType, oldSlug, newSlug string) error {
	if r.aliases == nil {
		r.aliases = map[string]string{}
	}
	r.aliases[chessType+"/"+oldSlug] = newSlug
	return nil
}

func newTestPositionService(repo *fakePositionRepo, alias *fakePositionAliasRepo) *chessLibraryService {
	return &chessLibraryService{
		repo:         repo,
		chessRefRepo: fakePositionChessRefRepo{},
		aliasRepo:    alias,
		indexer:      nil, // CHESS_KB_INDEX tắt trong test → indexer nil an toàn (mọi lời gọi indexer đều guard nil)
	}
}

func TestCreatePosition_NormalizesFENAndGeneratesSlug(t *testing.T) {
	svc := newTestPositionService(newFakePositionRepo(), &fakePositionAliasRepo{})
	// FEN CỤT (chỉ có bố cục quân) và KHÔNG có quân Vua — phải được chấp nhận,
	// bù đủ 6 trường, và suy ra side_to_move.
	p, err := svc.CreatePosition(context.Background(), &types.ChessPosition{
		TenantID: 1, Title: "Cách Tốt ăn quân", FEN: "8/8/8/3p4/4P3/8/8/8 w",
	})
	if err != nil {
		t.Fatalf("CreatePosition lỗi: %v", err)
	}
	if p.FEN != "8/8/8/3p4/4P3/8/8/8 w - - 0 1" {
		t.Errorf("FEN chưa được chuẩn hóa đủ 6 trường: %q", p.FEN)
	}
	if p.FENKey == "" {
		t.Errorf("FENKey phải được điền")
	}
	if p.SideToMove != "w" {
		t.Errorf("SideToMove = %q, muốn \"w\"", p.SideToMove)
	}
	if p.Slug == "" {
		t.Errorf("slug phải được sinh tự động")
	}
}

func TestCreatePosition_InvalidFEN_ReturnsError(t *testing.T) {
	svc := newTestPositionService(newFakePositionRepo(), &fakePositionAliasRepo{})
	_, err := svc.CreatePosition(context.Background(), &types.ChessPosition{
		TenantID: 1, Title: "Rác", FEN: "khong-phai-fen",
	})
	if err == nil {
		t.Fatalf("muốn lỗi FEN không hợp lệ nhưng không có")
	}
}

func TestCreatePosition_SlugCollision_AppendsSuffix(t *testing.T) {
	svc := newTestPositionService(newFakePositionRepo(), &fakePositionAliasRepo{})
	ctx := context.Background()
	first, err := svc.CreatePosition(ctx, &types.ChessPosition{
		TenantID: 1, Title: "Thế cờ mẫu", FEN: "8/8/8/8/8/8/8/8 w",
	})
	if err != nil {
		t.Fatalf("tạo lần 1 lỗi: %v", err)
	}
	second, err := svc.CreatePosition(ctx, &types.ChessPosition{
		TenantID: 1, Title: "Thế cờ mẫu", FEN: "8/8/8/8/8/8/8/8 b",
	})
	if err != nil {
		t.Fatalf("tạo lần 2 lỗi: %v", err)
	}
	if first.Slug == second.Slug {
		t.Fatalf("2 thế cờ trùng tiêu đề phải có slug KHÁC nhau, cả hai đều là %q", first.Slug)
	}
}

func TestGetPositionBySlug_ExactMatch(t *testing.T) {
	repo := newFakePositionRepo()
	svc := newTestPositionService(repo, &fakePositionAliasRepo{})
	created, err := svc.CreatePosition(context.Background(), &types.ChessPosition{
		TenantID: 1, Title: "Tàn cuộc Vua Tốt", FEN: "8/8/8/8/8/8/4P3/4K3 w",
	})
	if err != nil {
		t.Fatalf("CreatePosition lỗi: %v", err)
	}
	got, err := svc.GetPositionBySlug(context.Background(), 1, created.Slug)
	if err != nil {
		t.Fatalf("GetPositionBySlug khớp chính xác lỗi: %v", err)
	}
	if got.ID != created.ID {
		t.Errorf("trả nhầm bản ghi: got id=%s want id=%s", got.ID, created.ID)
	}
}

func TestGetPositionBySlug_ResolvesViaAlias(t *testing.T) {
	repo := newFakePositionRepo()
	alias := &fakePositionAliasRepo{}
	svc := newTestPositionService(repo, alias)
	created, err := svc.CreatePosition(context.Background(), &types.ChessPosition{
		TenantID: 1, Title: "Cấu trúc tốt cô lập", FEN: "8/8/8/8/8/8/4P3/4K3 w",
	})
	if err != nil {
		t.Fatalf("CreatePosition lỗi: %v", err)
	}
	// Giả lập đã đổi tên trước đó: alias "cau-truc-cu" -> slug hiện hành.
	if err := alias.AddAlias(context.Background(), 1, types.ChessRefTypePosition, "cau-truc-cu", created.Slug); err != nil {
		t.Fatalf("AddAlias lỗi: %v", err)
	}
	got, err := svc.GetPositionBySlug(context.Background(), 1, "cau-truc-cu")
	if err != nil {
		t.Fatalf("GetPositionBySlug qua alias lỗi: %v", err)
	}
	if got.ID != created.ID {
		t.Errorf("alias trả nhầm bản ghi: got id=%s want id=%s", got.ID, created.ID)
	}
}

func TestGetPositionBySlug_ResolvesViaFuzzy(t *testing.T) {
	repo := newFakePositionRepo()
	svc := newTestPositionService(repo, &fakePositionAliasRepo{})
	created, err := svc.CreatePosition(context.Background(), &types.ChessPosition{
		TenantID: 1, Title: "Tan Cuoc Co Ban", FEN: "8/8/8/8/8/8/4P3/4K3 w",
	})
	if err != nil {
		t.Fatalf("CreatePosition lỗi: %v", err)
	}
	// Slug gõ thiếu toàn bộ dấu gạch nối vẫn phải nắn được về đúng bản ghi
	// (bước 2 của resolveDeadSlug: so khớp sau khi bỏ '-'/'_').
	deadSlug := ""
	for _, r := range created.Slug {
		if r != '-' {
			deadSlug += string(r)
		}
	}
	if deadSlug == created.Slug {
		t.Skip("slug không có gạch nối để kiểm tra fuzzy — bỏ qua")
	}
	got, err := svc.GetPositionBySlug(context.Background(), 1, deadSlug)
	if err != nil {
		t.Fatalf("GetPositionBySlug qua fuzzy lỗi: %v", err)
	}
	if got.ID != created.ID {
		t.Errorf("fuzzy trả nhầm bản ghi: got id=%s want id=%s", got.ID, created.ID)
	}
}

func TestGetPositionBySlug_NotFound(t *testing.T) {
	svc := newTestPositionService(newFakePositionRepo(), &fakePositionAliasRepo{})
	if _, err := svc.GetPositionBySlug(context.Background(), 1, "khong-ton-tai"); err == nil {
		t.Fatalf("muốn lỗi not-found nhưng không có")
	}
}

func TestRenamePositionSlug_WritesAliasAndKeepsOldSlugResolvable(t *testing.T) {
	repo := newFakePositionRepo()
	alias := &fakePositionAliasRepo{}
	svc := newTestPositionService(repo, alias)
	created, err := svc.CreatePosition(context.Background(), &types.ChessPosition{
		TenantID: 1, Title: "Mô-típ ghim", FEN: "8/8/8/8/8/8/4P3/4K3 w",
	})
	if err != nil {
		t.Fatalf("CreatePosition lỗi: %v", err)
	}
	oldSlug := created.Slug

	renamed, err := svc.RenamePositionSlug(context.Background(), 1, created.ID, "the-co-moi")
	if err != nil {
		t.Fatalf("RenamePositionSlug lỗi: %v", err)
	}
	if renamed.Slug != "the-co-moi" {
		t.Errorf("slug mới = %q, muốn \"the-co-moi\"", renamed.Slug)
	}

	// Link cũ [[position/<oldSlug>]] vẫn phải giải mã đúng nhờ alias.
	got, err := svc.GetPositionBySlug(context.Background(), 1, oldSlug)
	if err != nil {
		t.Fatalf("GetPositionBySlug với slug cũ (qua alias) lỗi: %v", err)
	}
	if got.ID != created.ID {
		t.Errorf("slug cũ trả nhầm bản ghi sau khi đổi tên")
	}
}

func TestDeletePosition_RemovesRecord(t *testing.T) {
	repo := newFakePositionRepo()
	svc := newTestPositionService(repo, &fakePositionAliasRepo{})
	created, err := svc.CreatePosition(context.Background(), &types.ChessPosition{
		TenantID: 1, Title: "Xóa thử", FEN: "8/8/8/8/8/8/4P3/4K3 w",
	})
	if err != nil {
		t.Fatalf("CreatePosition lỗi: %v", err)
	}
	if err := svc.DeletePosition(context.Background(), 1, created.ID); err != nil {
		t.Fatalf("DeletePosition lỗi: %v", err)
	}
	if _, err := svc.GetPosition(context.Background(), 1, created.ID); err == nil {
		t.Fatalf("thế cờ vẫn còn sau khi xóa")
	}
}
