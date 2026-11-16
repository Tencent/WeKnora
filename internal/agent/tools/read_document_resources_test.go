package tools

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/access"
	"github.com/Tencent/WeKnora/internal/types"
)

const sharedImageEvidenceRef = "resource://AbCdEfGhIjKlMnOpQrStUv"

type (
	imageEvidenceLookups       struct{ allowed bool }
	deniedImageEvidenceMessage struct{}
)

func (deniedImageEvidenceMessage) GetMessage(context.Context, string, string) (*types.Message, error) {
	return nil, access.ErrForbidden
}

func (s imageEvidenceLookups) CheckTenantKBPermission(
	context.Context, string, uint64, types.TenantRole,
) (types.OrgMemberRole, bool, error) {
	return types.OrgRoleViewer, s.allowed, nil
}

func (s imageEvidenceLookups) GetKnowledgeBasesByIDsOnly(
	context.Context, []string,
) ([]*types.KnowledgeBase, error) {
	return []*types.KnowledgeBase{{ID: "shared", TenantID: 2}}, nil
}

func (s imageEvidenceLookups) IsReferencedByKnowledgeBase(
	context.Context, uint64, string, string,
) (bool, error) {
	return s.allowed, nil
}

func (s imageEvidenceLookups) ResolvePath(
	_ context.Context, reference string,
) (string, *types.StoredResource, error) {
	handle, _ := types.ParseResourcePath(reference)
	return "local://2/image.png", &types.StoredResource{
		Handle: handle, TenantID: 2, PhysicalPath: "local://2/image.png",
	}, nil
}

func roundTripImageEvidenceMessage(t *testing.T, message *types.Message) *types.Message {
	t.Helper()
	encoded, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	var reloaded types.Message
	if err := json.Unmarshal(encoded, &reloaded); err != nil {
		t.Fatal(err)
	}
	return &reloaded
}

// Exercise Execute, storage sanitization, JSON persistence, and authorization.
// Only repository, share and catalog lookups are faked; no model is needed.
func TestReadDocumentSharedImageEvidenceSurvivesPersistence(t *testing.T) {
	for _, source := range []string{"content", "image_info", "both"} {
		t.Run(source, func(t *testing.T) {
			ctx := types.WithCaller(context.Background(), types.Caller{
				TenantID: 1, UserID: "viewer", Role: types.TenantRoleContributor,
			})
			doc := &types.Knowledge{
				ID: "doc-1", TenantID: 2, KnowledgeBaseID: "shared", Title: "Image document",
			}
			chunk := &types.Chunk{
				ID: "chunk-1", TenantID: 2, KnowledgeID: doc.ID, KnowledgeBaseID: doc.KnowledgeBaseID,
				ChunkType: types.ChunkTypeText, IsEnabled: true, Content: "Chart description",
			}
			if source != "image_info" {
				chunk.Content += " ![chart](" + sharedImageEvidenceRef + ")"
			}
			if source != "content" {
				images, err := json.Marshal([]types.ImageInfo{{URL: sharedImageEvidenceRef, Caption: "Chart"}})
				if err != nil {
					t.Fatal(err)
				}
				chunk.ImageInfo = string(images)
			}
			tool := NewReadDocumentTool(
				&readDocKnowledgeService{docs: map[string]*types.Knowledge{doc.ID: doc}},
				&readDocChunkService{repo: &readDocChunkRepo{ordered: []*types.Chunk{chunk}}},
				types.SearchTargets{{
					Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: doc.KnowledgeBaseID, TenantID: 2,
				}},
			)
			result, err := tool.Execute(ctx, json.RawMessage(`{"id":"doc-1"}`))
			if err != nil || !result.Success {
				t.Fatalf("read_document failed: %v, %+v", err, result)
			}
			original, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			steps := []types.AgentStep{{ToolCalls: []types.ToolCall{{Name: ToolReadDocument, Result: result}}}}
			shared := imageEvidenceLookups{allowed: true}
			auth := access.MessageKBShareAuthorizer{ShareGuard: shared, KBs: shared, Bindings: shared}
			message := &types.Message{
				ID: "message", Role: "assistant", AgentID: "builtin-smart-reasoning", AgentTenantID: 1,
				Content: "![chart](" + sharedImageEvidenceRef + ")", AgentSteps: steps,
			}
			authorize := func(msg *types.Message, ref string, a access.MessageKBShareAuthorizer) error {
				_, err := access.AuthorizeMessageFile(ctx, msg, ref, nil, shared, a)
				return err
			}
			// A full result survives the normal JSON representation even before this fix.
			if err := authorize(roundTripImageEvidenceMessage(t, message), sharedImageEvidenceRef, auth); err != nil {
				t.Fatalf("uncompacted retrieved evidence must authorize: %v", err)
			}
			message.AgentSteps = SanitizeAgentStepsForStorage(steps)
			if err := authorize(message, sharedImageEvidenceRef, auth); err != nil {
				t.Errorf("retrieved shared image must remain accessible before reload: %v", err)
			}
			reloaded := roundTripImageEvidenceMessage(t, message)
			if err := authorize(reloaded, sharedImageEvidenceRef, auth); err != nil {
				t.Errorf("retrieved shared image must remain accessible after reload: %v", err)
			}
			after, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			if string(original) != string(after) {
				t.Error("sanitizer mutated the original model result")
			}
			if _, exists := reloaded.AgentSteps[0].ToolCalls[0].Result.Data["chunks"]; exists {
				t.Error("full chunk payload must remain omitted")
			}
			// Each negative uses the new compact evidence, not KnowledgeReferences.
			noBinding := auth
			noBinding.Bindings = imageEvidenceLookups{allowed: false}
			if err := authorize(reloaded, sharedImageEvidenceRef, noBinding); !errors.Is(err, access.ErrForbidden) {
				t.Fatalf("missing live binding must deny: %v", err)
			}
			noShare := auth
			noShare.ShareGuard = imageEvidenceLookups{allowed: false}
			if err := authorize(reloaded, sharedImageEvidenceRef, noShare); !errors.Is(err, access.ErrForbidden) {
				t.Fatalf("revoked share must deny: %v", err)
			}
			unrelated := *reloaded
			other := "resource://ZbCdEfGhIjKlMnOpQrStUv"
			unrelated.Content += " ![unretrieved](" + other + ")"
			if err := authorize(&unrelated, other, auth); !errors.Is(err, access.ErrForbidden) {
				t.Fatalf("unretrieved image must deny even with a live binding: %v", err)
			}
			proseOnly := *reloaded
			proseOnly.AgentSteps = nil
			if err := authorize(&proseOnly, sharedImageEvidenceRef, auth); !errors.Is(err, access.ErrForbidden) {
				t.Fatalf("model prose alone must not authorize: %v", err)
			}
			// The file proxy must stop when the session/message lookup denies access.
			_, err = access.ResolveMessageFile(ctx, "other-session", "other-message", sharedImageEvidenceRef,
				deniedImageEvidenceMessage{}, nil, shared, auth)
			if err == nil {
				t.Fatal("denied message lookup must not yield file access")
			}
		})
	}
}

func TestReadDocumentResourceReferencesAreCompactAndCanonical(t *testing.T) {
	other := "resource://ZbCdEfGhIjKlMnOpQrStUv"
	hidden := "resource://YbCdEfGhIjKlMnOpQrStUv"
	images, err := json.Marshal([]types.ImageInfo{
		{URL: sharedImageEvidenceRef, Caption: hidden, OriginalURL: hidden, OCRText: hidden},
		{URL: other},
		{URL: "https://example.com/a.png"},
		{URL: sharedImageEvidenceRef + "longer"},
	})
	if err != nil {
		t.Fatal(err)
	}
	rows := []readChunkRow{
		{chunk: &types.Chunk{
			Content:   sharedImageEvidenceRef + " " + sharedImageEvidenceRef + " " + sharedImageEvidenceRef + "longer",
			ImageInfo: string(images),
		}},
		{chunk: &types.Chunk{Content: sharedImageEvidenceRef, ImageInfo: "invalid JSON"}},
	}
	tool := &ReadDocumentTool{}
	doc := &types.Knowledge{ID: "doc", KnowledgeBaseID: "kb"}
	refs, _ := tool.buildData(doc, 2, rows)["resource_references"].([]map[string]interface{})
	if len(refs) != 2 || refs[0]["url"] != sharedImageEvidenceRef || refs[1]["url"] != other {
		t.Fatalf("expected two unique canonical returned resources, got %#v", refs)
	}
	for _, ref := range refs {
		if len(ref) != 1 {
			t.Fatalf("resource evidence must not retain text: %#v", ref)
		}
	}
	empty := []readChunkRow{{chunk: &types.Chunk{Content: "no image", ImageInfo: "bad JSON"}}}
	got, _ := tool.buildData(doc, 1, empty)["resource_references"].([]map[string]interface{})
	if len(got) != 0 {
		t.Fatalf("unexpected evidence: %#v", got)
	}
}
