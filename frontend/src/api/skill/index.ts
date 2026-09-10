import { del, get, post, postUpload } from "../../utils/request";
import type { ConfigSkillFileContent, ConfigSkillFileEntry } from "../system";

// Skill信息
export interface SkillInfo {
  source?: "builtin";
  version?: string;
  name: string;
  description: string;
}

export interface SkillCatalogInstall {
	version?: string;
  skill_id: string;
  sandbox_config_id: string;
  sandbox_config_name?: string;
  sandbox_type?: string;
  status: string;
  enabled: boolean;
  error?: string;
  bundle_sha256?: string;
  updated_at: string;
}

export interface SkillCatalogItem {
  builtin?: boolean;
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

export interface BuiltinSkillsSummary {
  known: boolean;
  version?: string;
  skills: SkillInfo[];
  unavailable: number;
}

// 获取当前沙箱配置上可执行的 Skills；未传 sandboxConfigId 或
// skills_available 为 false 时，前端应隐藏/禁用 Skills 配置
export function listSkills(sandboxConfigId?: string, sessionId?: string) {
  return get<{ data: SkillInfo[]; skills_available?: boolean; builtin_skills?: BuiltinSkillsSummary }>('/api/v1/skills', {
    params: { sandbox_config_id: sandboxConfigId || undefined, session_id: sessionId || undefined },
  });
}

export function listSkillCatalog() {
  return get<{ data: SkillCatalogItem[] }>('/api/v1/skills/catalog');
}

export function registerSkillCatalogFromSource(source: string) {
  return post<{ data: SkillCatalogRegisterResult }>('/api/v1/skills/catalog', { source }, {
    timeout: 2 * 60 * 1000,
  });
}

export function installSkillFromPrompt(prompt: string, sandboxConfigIds: string[]) {
  return post<{ data: { installs: Record<string, string>; errors?: Record<string, string> } }>(
    '/api/v1/skills/catalog/install-prompt', { prompt, sandbox_config_ids: sandboxConfigIds },
  );
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

export interface DiscoverySkill {
	/** SHA-256 of the reproducible install archive, distinct from the file manifest digest. */
	bundle_sha256?: string
  runtime: 'sandbox' | 'publisher' | 'local_browser'
  id: string
  name: string
  title: Record<string, string>
  description: Record<string, string>
  category: 'office' | 'analysis' | 'browser'
  distribution: 'builtin' | 'community' | 'external_link'
  install_source?: string
  publisher: string
  license: string
  source_url: string
  license_url: string
  docs_url?: string
  version?: string
  digest?: string
}

export function listSkillDiscovery() {
  return get<{ data: DiscoverySkill[] }>('/api/v1/skills/discovery')
}

export function registerBuiltinSkill(id: string, replaceExisting = false) {
  return post<{ data: SkillCatalogRegisterResult }>(`/api/v1/skills/discovery/${encodeURIComponent(id)}/register`, {
    replace_existing: replaceExisting,
  })
}
