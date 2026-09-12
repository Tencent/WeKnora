package service

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/sandbox"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestSkillPromptValidationBeforeStartingWork(t *testing.T) {
	svc := &TenantSkillService{}
	for _, prompt := range []string{"  ", strings.Repeat("中", MaxSkillPromptRunes+1)} {
		_, err := svc.InstallSkillsFromPrompt(context.Background(), 7, prompt, []string{"cfg-1"})
		require.ErrorContains(t, err, "1 to 4000")
	}
	_, err := svc.InstallSkillsFromPrompt(context.Background(), 7, "Install ID 3044", nil)
	require.ErrorContains(t, err, "1 to 20")
}

func TestPromptInstallFollowsAdministratorSourceInsteadOfForcingRegistrySearch(t *testing.T) {
	request := "根据 https://mirrors.tencent.com/repository/generic/knot-skill/install.md 安装 ID 3044"
	prompt := buildPromptInstallPrompt(
		"/opt/weknora/tenant/skills/prompt-install-123", request, map[string]string{"curl": "/usr/bin/curl"},
	)
	require.Contains(t, prompt, request)
	require.Contains(t, prompt, "follow that source directly")
	require.Contains(t, prompt, "Do not force a ClawHub search")
	require.Contains(t, prompt, "unavailable credentials")
	require.Contains(t, prompt, "staging directory")
	require.Contains(t, prompt, "Do not create a Python venv")
}

func TestPendingPromptRequiresUnregisteredRequestMarker(t *testing.T) {
	row := &types.TenantSkillEntity{Name: "prompt-install-123", Instructions: promptInstallPrefix + "install skill"}
	require.Equal(t, "install skill", pendingSkillPrompt(row))
	row.CatalogID = "real-catalog"
	require.Empty(t, pendingSkillPrompt(row))
	row.CatalogID = ""
	row.BundleSHA256 = "real-bundle"
	require.Empty(t, pendingSkillPrompt(row))
	row.BundleSHA256 = ""
	row.Instructions = "regular skill instructions"
	require.Empty(t, pendingSkillPrompt(row))
}

func TestSkillPromptSelectionPreservesExistingSameNameCatalog(t *testing.T) {
	fx := newInstallFixture(t)
	ctx := context.Background()
	original := &types.TenantSkillCatalogEntity{ID: "mine", TenantID: 7, Name: "slides", BundleSHA256: "mine"}
	require.NoError(t, fx.skillRepo.CreateCatalog(ctx, original))
	_, err := fx.svc.createCatalogFromValidatedBundle(ctx, 7, &SkillBundle{Name: "slides", SHA256: "other"}, nil)
	require.Error(t, err)
	stored, err := fx.skillRepo.GetCatalogByName(ctx, 7, "slides")
	require.NoError(t, err)
	require.Equal(t, original, stored)
}

func TestPromptInstallRejectsOtherTenantSandboxBeforeCreatingJob(t *testing.T) {
	fx := newInstallFixture(t)
	result, err := fx.svc.InstallSkillsFromPrompt(context.Background(), 8, "install skill", []string{"cfg-1"})
	require.NoError(t, err)
	require.Empty(t, result.Installs)
	require.Contains(t, result.Errors, "cfg-1")
}

func promptInstallFixture(t *testing.T) (*installFixture, *SkillBundle, string) {
	fx := newInstallFixture(t)
	bundle := &SkillBundle{
		Name: "prompt-install-test", Description: "Install from documentation",
		Instructions: promptInstallPrefix + "Install ID 3044 according to its documentation",
	}
	row, err := fx.skillRepo.GetSkill(context.Background(), 7, "cfg-1", "sk-1")
	require.NoError(t, err)
	row.Name = bundle.Name
	row.Instructions = bundle.Instructions
	row.BundleSHA256 = ""
	require.NoError(t, fx.skillRepo.UpdateSkill(context.Background(), row))
	directory, err := sandbox.SkillDirFor(bundle.Name)
	require.NoError(t, err)
	fx.sandboxMgr.files = map[string][]byte{}
	return fx, bundle, directory
}

func TestPromptInstallAcquiresValidatesAndSnapshotsRealSkill(t *testing.T) {
	fx, pending, dir := promptInstallFixture(t)
	archive := zipBundle(t, map[string]string{"SKILL.md": validSkillMD, "scripts/extract.py": "print('hi')\n"})
	fx.sandboxMgr.files[dir+".zip"] = archive
	err := fx.svc.runInstall(context.Background(), 7, "cfg-1", "sk-1", pending)
	require.NoError(t, err)
	require.Len(t, fx.agentPrompts, 2)
	require.Contains(t, fx.agentPrompts[0], "Install ID 3044")
	require.Contains(t, fx.agentPrompts[1], installSkillDir)
	row, err := fx.skillRepo.GetSkill(context.Background(), 7, "cfg-1", "sk-1")
	require.NoError(t, err)
	require.Equal(t, "pdf-tools", row.Name)
	require.Equal(t, types.SkillStatusReady, row.Status)
	require.NotEmpty(t, row.CatalogID)
	require.NotEmpty(t, row.BundleSHA256)
	require.Contains(t, fx.events, "verify-python")
	require.Contains(t, fx.events, "create-snapshot")
	require.Contains(t, fx.events, "switch-pointer")
	require.Equal(t, "snap-1", fx.configRepo.saved.Config.SkillImage.SnapshotID)
}

func TestPromptInstallInvalidOrMissingBundleNeverPublishes(t *testing.T) {
	for _, body := range [][]byte{nil, []byte("invalid zip")} {
		fx, pending, dir := promptInstallFixture(t)
		if body != nil {
			fx.sandboxMgr.files[dir+".zip"] = body
		}
		err := fx.svc.runInstall(context.Background(), 7, "cfg-1", "sk-1", pending)
		require.Error(t, err)
		require.NotContains(t, fx.events, "create-snapshot")
		require.NotContains(t, fx.events, "switch-pointer")
		row, err := fx.skillRepo.GetSkill(context.Background(), 7, "cfg-1", "sk-1")
		require.NoError(t, err)
		require.Equal(t, types.SkillStatusFailed, row.Status)
		require.Empty(t, row.CatalogID)
		require.Contains(t, pendingSkillPrompt(row), "3044", "failed acquisition retains retry instructions")
	}
}

func TestPromptInstallAgentErrorPreservesCauseAndNeverPublishes(t *testing.T) {
	fx, pending, _ := promptInstallFixture(t)
	fx.agentErr = fmt.Errorf("missing Taihu token")
	err := fx.svc.runInstall(context.Background(), 7, "cfg-1", "sk-1", pending)
	require.ErrorContains(t, err, "missing Taihu token")
	require.NotContains(t, fx.events, "create-snapshot")
	row, err := fx.skillRepo.GetSkill(context.Background(), 7, "cfg-1", "sk-1")
	require.NoError(t, err)
	require.Contains(t, row.Error, "missing Taihu token")
}

func TestPromptSkillArchiveScriptExcludesEnvironmentsAndRejectsLinks(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is unavailable")
	}
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "SKILL.md"), []byte(validSkillMD), 0o600))
	require.NoError(t, os.Mkdir(filepath.Join(root, "node_modules"), 0o700))
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "node_modules", "dependency.js"), []byte("large dependency"), 0o600,
	))
	output := filepath.Join(t.TempDir(), "skill.zip")
	run := func() error {
		return exec.Command(
			python, "-c", promptSkillArchiveScript, root, output, "100", "100000", "1000000", "1000000",
		).Run()
	}
	require.NoError(t, run())
	data, err := os.ReadFile(output)
	require.NoError(t, err)
	bundle, err := ParseSkillBundle(data)
	require.NoError(t, err)
	require.Len(t, bundle.Files, 1)
	require.NoError(t, os.Symlink(filepath.Join(root, "SKILL.md"), filepath.Join(root, "linked.md")))
	require.Error(t, run())
}
