import { del, get, post, postUpload } from "../../utils/request";
import type { ConfigSkillFileContent, ConfigSkillFileEntry } from "../system";

// Skill信息
export interface SkillInfo {
  name: string;
  description: string;
}

export interface SkillCatalogInstall {
  skill_id: string;
  sandbox_config_id: string;
  sandbox_config_name?: string;
  sandbox_type?: string;
  status: string;
  enabled: boolean;
  error?: string;
  version?: string;
  bundle_sha256?: string;
  // Present while a newer install is in flight or has failed and the sandbox
  // still runs the previous version.
  served?: { version?: string };
  updated_at: string;
}

export interface SkillCatalogItem {
  id: string;
  name: string;
  version?: string;
  description?: string;
  bundle_sha256?: string;
  created_at: string;
  updated_at: string;
  installations: SkillCatalogInstall[];
}

export interface SkillCatalogRegisterResult {
  id: string;
  name: string;
  version?: string;
  description?: string;
}

/** Locates a shared agent's source workspace, same parameters as a chat request. */
export interface AgentScope {
  agentId?: string;
  /** Set only for a shared agent; left empty for this workspace's own agents. */
  sourceTenantId?: string | number;
}

// Lists the skills executable on the current sandbox config. Without a
// sandboxConfigId, or when skills_available is false, the caller should hide or
// disable the skills UI.
//
// Pass the agent for a shared one: its skills are installed on a sandbox config
// in the OWNER's workspace, so looking them up in the caller's returns nothing.
// The backend then reads the config off that agent and ignores sandboxConfigId.
export function listSkills(sandboxConfigId?: string, agent?: AgentScope) {
  const params: Record<string, string> = {};
  if (sandboxConfigId) params.sandbox_config_id = sandboxConfigId;
  if (agent?.agentId && agent?.sourceTenantId) {
    params.agent_id = agent.agentId;
    params.agent_source_tenant_id = String(agent.sourceTenantId);
  }
  return get<{ data: SkillInfo[]; skills_available?: boolean }>('/api/v1/skills', { params });
}

export function listSkillCatalog() {
  return get<{ data: SkillCatalogItem[] }>('/api/v1/skills/catalog');
}

export function registerSkillCatalogFromSource(source: string) {
  return post<{ data: SkillCatalogRegisterResult }>('/api/v1/skills/catalog', { source }, {
    timeout: 2 * 60 * 1000,
  });
}

export function registerSkillCatalogFromFile(
  file: File,
  onProgress?: (percent: number) => void,
) {
  const form = new FormData();
  form.append('file', file);
  return postUpload('/api/v1/skills/catalog', form, (e: any) => {
    if (e.total) onProgress?.(Math.round((e.loaded * 100) / e.total));
  }, { timeout: 5 * 60 * 1000 }) as Promise<{ data: SkillCatalogRegisterResult }>;
}

export function installSkillCatalog(catalogId: string, sandboxConfigIds: string[]) {
  return post<{ data: { installs: Record<string, string>; errors?: Record<string, string> } }>(
    `/api/v1/skills/catalog/${catalogId}/install`,
    { sandbox_config_ids: sandboxConfigIds },
  );
}

export function deleteSkillCatalog(catalogId: string) {
  return del(`/api/v1/skills/catalog/${catalogId}`);
}

export function listCatalogSkillFiles(catalogId: string) {
  return get<{ data: ConfigSkillFileEntry[] }>(`/api/v1/skills/catalog/${catalogId}/files`);
}

export function getCatalogSkillFile(catalogId: string, path: string) {
  return get<{ data: ConfigSkillFileContent }>(`/api/v1/skills/catalog/${catalogId}/files/content`, {
    params: { path },
  });
}
