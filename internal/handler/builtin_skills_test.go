package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"

	builtin "github.com/Tencent/WeKnora/internal/builtin/skills"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type builtinPickerLister struct{ fakeUsableSkillLister }

func (*builtinPickerLister) ListBuiltinSkills(context.Context, uint64, string) []builtin.Entry {
	return builtin.CompatibleEntries(builtin.PublishedManifest())
}

func TestSkillPickerMergesPreinstalledSkillsWithoutRegistration(t *testing.T) {
	lister := &builtinPickerLister{
		fakeUsableSkillLister: fakeUsableSkillLister{
			skills: []*types.TenantSkillEntity{{Name: "pdf", Description: "workspace override"}},
		},
	}
	router := newChatSkillRouter(NewSkillHandler(lister, nil))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/skills?sandbox_config_id=cfg-office", nil))
	require.Equal(t, 200, w.Code)
	var body struct {
		Data []SkillInfoResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Len(t, body.Data, 8)
	require.Equal(t, SkillInfoResponse{Name: "pdf", Description: "workspace override"}, body.Data[0])
	for _, entry := range body.Data[1:] {
		require.Equal(t, "builtin", entry.Source)
		expectedVersion := "2026.09.1"
		if entry.Name == "browser" {
			expectedVersion = builtin.Version
		}
		require.Equal(t, expectedVersion, entry.Version)
	}
}

type manifestPickerLister struct {
	fakeUsableSkillLister
	manifest *types.BuiltinSkillsManifest
	reads    int
}

func (l *manifestPickerLister) GetBuiltinSkillsManifest(context.Context, uint64, string) *types.BuiltinSkillsManifest {
	l.reads++
	return l.manifest
}

func TestSkillSummaryDistinguishesUnknownEmptyAndOverriddenBuiltins(t *testing.T) {
	for _, tc := range []struct {
		name     string
		manifest *types.BuiltinSkillsManifest
		known    bool
		count    int
	}{
		{name: "unknown"},
		{
			name: "declared empty", manifest: &types.BuiltinSkillsManifest{SchemaVersion: 1, Version: "empty"},
			known: true,
		},
		{name: "workspace override", manifest: builtin.PublishedManifest(), known: true, count: 8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lister := &manifestPickerLister{
				manifest: tc.manifest,
				fakeUsableSkillLister: fakeUsableSkillLister{
					skills: []*types.TenantSkillEntity{{Name: "pdf", Description: "workspace override"}},
				},
			}
			router := newChatSkillRouter(NewSkillHandler(lister, nil))
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest("GET", "/skills?sandbox_config_id=cfg-office", nil))
			require.Equal(t, 200, w.Code)
			var body struct {
				Data    []SkillInfoResponse  `json:"data"`
				Builtin BuiltinSkillsSummary `json:"builtin_skills"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
			require.Equal(t, tc.known, body.Builtin.Known)
			require.Len(t, body.Builtin.Skills, tc.count)
			require.Equal(t, "workspace override", body.Data[0].Description)
			require.Equal(t, 1, lister.reads, "the picker and card summary share one metadata read")
		})
	}
}

type sessionPickerLister struct {
	manifestPickerLister
	sessionID string
	fail      bool
}

func (l *sessionPickerLister) ListSessionSkillResources(
	_ context.Context,
	_ uint64,
	sessionID, _ string,
) ([]*types.TenantSkillEntity, *types.BuiltinSkillsManifest, error) {
	l.sessionID = sessionID
	if l.fail {
		return nil, nil, fmt.Errorf("not owned")
	}
	manifest := builtin.PublishedManifest()
	manifest.Skills = manifest.Skills[:1]
	return nil, manifest, nil
}

func TestChatPickerUsesSessionImageAndDoesNotFallBackOnOwnershipFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		lister := &sessionPickerLister{fail: fail}
		router := newChatSkillRouter(NewSkillHandler(lister, nil))
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("GET", "/skills?sandbox_config_id=other&session_id=old-session", nil))
		require.Equal(t, "old-session", lister.sessionID)
		require.Zero(t, lister.reads, "must not inspect agent's new template")
		require.Empty(t, lister.configID, "must not use the agent's installed set")
		if fail {
			require.Equal(t, 404, w.Code)
			continue
		}
		require.Equal(t, 200, w.Code)
		var body struct {
			Data []SkillInfoResponse `json:"data"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
		require.Len(t, body.Data, 1)
		require.Equal(t, "builtin", body.Data[0].Source)
	}
}
