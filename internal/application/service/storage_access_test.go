package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

type stubKBRepoForStorage struct {
	kbs map[string]*types.KnowledgeBase
}

func (s *stubKBRepoForStorage) GetKnowledgeBaseByID(_ context.Context, id string) (*types.KnowledgeBase, error) {
	if kb, ok := s.kbs[id]; ok {
		return kb, nil
	}
	return nil, nil
}

type stubKGRepoForStorage struct {
	byFilePath map[string][]string
}

func (s *stubKGRepoForStorage) ListKnowledgeBaseIDsByFilePath(_ context.Context, _ uint64, filePath string) ([]string, error) {
	return s.byFilePath[filePath], nil
}

type stubChunkRepoForStorage struct {
	byRef map[string][]string
}

func (s *stubChunkRepoForStorage) ListKnowledgeBaseIDsByStorageReference(_ context.Context, _ uint64, ref string) ([]string, error) {
	return s.byRef[ref], nil
}

type stubKBShareForStorage struct {
	shared map[string]bool
}

func (s *stubKBShareForStorage) CheckTenantKBPermission(_ context.Context, kbID string, _ uint64, _ types.TenantRole) (types.OrgMemberRole, bool, error) {
	if s.shared[kbID] {
		return types.OrgRoleViewer, true, nil
	}
	return "", false, nil
}

func TestAuthorizeSharedStoragePathSameTenant(t *testing.T) {
	auth := NewStorageAccessAuthorizer(nil, nil, &stubKBRepoForStorage{}, nil, nil)
	got, err := auth.AuthorizeSharedStoragePath(
		context.Background(), 42, types.TenantRoleOwner,
		"local://42/exports/a.png", "", "",
	)
	if err != nil {
		t.Fatalf("AuthorizeSharedStoragePath() err = %v", err)
	}
	if got.OwnerTenantID != 42 {
		t.Fatalf("OwnerTenantID = %d, want 42", got.OwnerTenantID)
	}
}

func TestAuthorizeSharedStoragePathViaSharedKB(t *testing.T) {
	const (
		kbID      = "kb-shared"
		filePath  = "local://10008/exports/a.png"
		callerTID = uint64(10005)
		ownerTID  = uint64(10008)
	)
	auth := NewStorageAccessAuthorizer(
		&stubKBShareForStorage{shared: map[string]bool{kbID: true}},
		nil,
		&stubKBRepoForStorage{kbs: map[string]*types.KnowledgeBase{
			kbID: {ID: kbID, TenantID: ownerTID},
		}},
		&stubKGRepoForStorage{byFilePath: map[string][]string{}},
		&stubChunkRepoForStorage{byRef: map[string][]string{filePath: {kbID}}},
	)
	got, err := auth.AuthorizeSharedStoragePath(
		context.Background(), callerTID, types.TenantRoleOwner, filePath, "", "",
	)
	if err != nil {
		t.Fatalf("AuthorizeSharedStoragePath() err = %v", err)
	}
	if got.OwnerTenantID != ownerTID {
		t.Fatalf("OwnerTenantID = %d, want %d", got.OwnerTenantID, ownerTID)
	}
}

func TestAuthorizeSharedStoragePathDeniedWithoutShare(t *testing.T) {
	const (
		kbID     = "kb-private"
		filePath = "local://10008/exports/a.png"
	)
	auth := NewStorageAccessAuthorizer(
		&stubKBShareForStorage{shared: map[string]bool{}},
		nil,
		&stubKBRepoForStorage{kbs: map[string]*types.KnowledgeBase{
			kbID: {ID: kbID, TenantID: 10008},
		}},
		nil,
		&stubChunkRepoForStorage{byRef: map[string][]string{filePath: {kbID}}},
	)
	_, err := auth.AuthorizeSharedStoragePath(
		context.Background(), 10005, types.TenantRoleOwner, filePath, "", "",
	)
	if err == nil {
		t.Fatal("expected error for unshared KB")
	}
}

func TestAuthorizeSharedStoragePathWithKBHint(t *testing.T) {
	const kbID = "kb-hint"
	auth := NewStorageAccessAuthorizer(
		&stubKBShareForStorage{shared: map[string]bool{kbID: true}},
		nil,
		&stubKBRepoForStorage{kbs: map[string]*types.KnowledgeBase{
			kbID: {ID: kbID, TenantID: 10008},
		}},
		nil,
		nil,
	)
	got, err := auth.AuthorizeSharedStoragePath(
		context.Background(), 10005, types.TenantRoleOwner,
		"local://10008/exports/a.png", kbID, "",
	)
	if err != nil {
		t.Fatalf("AuthorizeSharedStoragePath() err = %v", err)
	}
	if got.OwnerTenantID != 10008 {
		t.Fatalf("OwnerTenantID = %d, want 10008", got.OwnerTenantID)
	}
}
