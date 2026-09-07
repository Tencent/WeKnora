package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
)

// ToolInstallSkill is the chat entry point to the skill image lifecycle.
const ToolInstallSkill = "install_skill"

// SkillInstaller delegates to the same image lifecycle used by skill management.
type SkillInstaller interface {
	InstallSkillFromSource(context.Context, uint64, string, string) (string, error)
	InstallSkill(context.Context, uint64, string, []byte) (string, error)
	GetSkill(context.Context, uint64, string, string) (*types.TenantSkillEntity, error)
}

// CanInstallSkills mirrors sandbox-config routes, including their full-access
// API key gate. A viewer role on an API-key principal is not its authority.
func CanInstallSkills(ctx context.Context) bool {
	if scope, ok := types.TenantAPIKeyScopeFromContext(ctx); ok {
		return scope.FullAccess
	}
	return types.TenantRoleFromContext(ctx).HasPermission(types.TenantRoleAdmin)
}

// InstallSkillTool installs and inspects skills on a fixed workspace sandbox configuration.
type InstallSkillTool struct {
	BaseTool
	installer SkillInstaller
	tenantID  uint64
	configID  string
	files     SandboxFileSource
}

// NewInstallSkillTool binds installation to the conversation's resolved sandbox configuration.
func NewInstallSkillTool(installer SkillInstaller, tenantID uint64, configID string) *InstallSkillTool {
	return &InstallSkillTool{
		BaseTool: NewBaseTool(ToolInstallSkill,
			"Install a user-requested skill into this conversation's sandbox configuration, "+
				"or check its installation status. Read skill://skill-installer/SKILL.md first. "+
				"Installation is shared with other conversations using the configuration and runs asynchronously. "+
				"Never treat acceptance as completion.",
			json.RawMessage(`{
  "type": "object",
  "properties": {
    "action": {"type": "string", "enum": ["install", "status"], "description": "Defaults to install"},
    "source": {
      "type": "string",
      "description": "User-supplied registry reference or full URL; install requires either source or archive_path"
    },
    "archive_path": {
      "type": "string",
      "description": "Absolute path of the user-attached ZIP under /workspace/input, from the attachment context"
    },
    "skill_id": {"type": "string", "description": "Returned installation ID, required for status"}
  },
  "additionalProperties": false
}`)),
		installer: installer, tenantID: tenantID, configID: configID,
	}
}

// WithAttachments enables installation from ZIP files staged in the current session.
func (t *InstallSkillTool) WithAttachments(files SandboxFileSource) *InstallSkillTool {
	t.files = files
	return t
}

func (t *InstallSkillTool) installArchive(ctx context.Context, archivePath string) (string, error) {
	sessionID := resolveSessionID(ctx)
	if t.files == nil || sessionID == "" {
		return "", fmt.Errorf("chat attachment access is unavailable")
	}
	if archivePath != path.Clean(archivePath) || !strings.HasPrefix(archivePath, sandbox.SessionInputRoot+"/") ||
		strings.ContainsAny(archivePath, "\\\x00") || !strings.EqualFold(path.Ext(archivePath), ".zip") {
		return "", fmt.Errorf("archive_path must be the attached ZIP's absolute path under /workspace/input")
	}
	stat, err := t.files.StatSessionFile(ctx, sessionID, archivePath)
	if err != nil {
		return "", err
	}
	if stat == nil || stat.Type != sandbox.RemoteEntryFile {
		return "", fmt.Errorf("skill attachment must be a regular ZIP file")
	}
	limit := utils.GetMaxSkillBundleSize()
	if stat.Size <= 0 || stat.Size > limit {
		return "", fmt.Errorf("skill attachment size must be between 1 and %d bytes", limit)
	}
	archive, err := t.files.ReadSessionFile(ctx, sessionID, archivePath)
	if err != nil {
		return "", err
	}
	if int64(len(archive)) > limit {
		return "", fmt.Errorf("skill attachment exceeds %d bytes", limit)
	}
	return t.installer.InstallSkill(ctx, t.tenantID, t.configID, archive)
}

// Execute checks caller authority before submitting an install or reading its status.
func (t *InstallSkillTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	fail := func(message string) (*types.ToolResult, error) {
		return &types.ToolResult{Success: false, Error: message}, nil
	}
	tenantID, _ := types.TenantIDFromContext(ctx)
	if !CanInstallSkills(ctx) || tenantID == 0 || tenantID != t.tenantID {
		return fail("skill installation requires administrator access to this workspace")
	}
	if t.installer == nil || t.configID == "" || t.configID == "-" {
		return fail("select a sandbox configuration before installing skills")
	}
	var input struct {
		Action      string `json:"action"`
		Source      string `json:"source"`
		SkillID     string `json:"skill_id"`
		ArchivePath string `json:"archive_path"`
	}
	if err := json.Unmarshal(args, &input); err != nil {
		return fail("invalid skill installation arguments")
	}
	input.Source, input.SkillID = strings.TrimSpace(input.Source), strings.TrimSpace(input.SkillID)
	input.ArchivePath = strings.TrimSpace(input.ArchivePath)
	accepted := false
	switch input.Action {
	case "", "install":
		if (input.Source == "") == (input.ArchivePath == "") || input.SkillID != "" {
			return fail("install requires exactly one of source or archive_path, and no skill_id")
		}
		var id string
		var err error
		if input.ArchivePath != "" {
			id, err = t.installArchive(ctx, input.ArchivePath)
		} else {
			id, err = t.installer.InstallSkillFromSource(ctx, t.tenantID, t.configID, input.Source)
		}
		if err != nil {
			return fail(err.Error())
		}
		input.SkillID, accepted = id, true
	case "status":
		if input.SkillID == "" || input.Source != "" || input.ArchivePath != "" {
			return fail("status requires skill_id and no source")
		}
	default:
		return fail("action must be install or status")
	}
	data := map[string]interface{}{"sandbox_config_id": t.configID, "skill_id": input.SkillID}
	skill, err := t.installer.GetSkill(ctx, t.tenantID, t.configID, input.SkillID)
	if err != nil || skill == nil {
		if !accepted {
			if err != nil {
				return fail(err.Error())
			}
			return fail("skill installation not found in this sandbox configuration")
		}
		// The mutation already succeeded. A failed status read must not invite
		// another install and accidentally replace the in-flight operation.
		data["status"] = "accepted"
	} else {
		// Do not serialize the entity: it contains environment values and
		// internal storage references which are not chat output.
		data["name"], data["status"], data["error"] = skill.Name, skill.Status, skill.Error
	}
	output, _ := json.Marshal(data)
	return &types.ToolResult{Success: true, Data: data, Output: fmt.Sprintf(
		"%s\nReport the actual status in this conversation. Installation progress is shown inline; "+
			"use action=status to check again when asked. Ready skills follow the sandbox rollout policy "+
			"and the agent's skill selection; they may require a new conversation.", output,
	)}, nil
}
