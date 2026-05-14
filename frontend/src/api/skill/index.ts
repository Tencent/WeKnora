import { get, post, postUpload, del, getDown } from "../../utils/request";

// Skill信息
export interface SkillInfo {
  name: string;
  description: string;
  source?: string; // "preloaded" | "installed" | "url"
}

// Skill详情
export interface SkillDetail {
  name: string;
  description: string;
  source: string;
  instructions: string;
  files: string[];
  docs: { path: string; content: string }[];
}

// Artifact元数据
export interface ArtifactMeta {
  filename: string;
  mime_type: string;
  name: string;
  versions: number;
  size: number;
}

// 获取Skills列表（包括预装和已安装的）
export function listSkills() {
  return get<{ data: SkillInfo[]; skills_available?: boolean; skills_hub_available?: boolean }>('/api/v1/skills');
}

// 获取Skill详情
export function getSkillDetail(name: string) {
  return get<{ data: SkillDetail }>(`/api/v1/skills/${encodeURIComponent(name)}`);
}

// 从URL安装Skill
export function installSkill(name: string, url: string) {
  return post<{ message: string }>('/api/v1/skills/install', { name, url });
}

// 上传安装Skill
export function uploadSkill(name: string, file: File) {
  const formData = new FormData();
  formData.append('name', name);
  formData.append('file', file);
  return postUpload('/api/v1/skills/upload', formData);
}

// 卸载Skill
export function uninstallSkill(name: string) {
  return del<{ message: string }>(`/api/v1/skills/${encodeURIComponent(name)}`);
}

// 刷新Skills
export function refreshSkills() {
  return post<{ message: string }>('/api/v1/skills/refresh', {});
}

// 导出Skill（返回下载URL）
export function getSkillExportUrl(name: string) {
  return `/api/v1/skills/${encodeURIComponent(name)}/export`;
}

// 下载Skill（带认证的 blob 下载）
export async function downloadSkill(name: string) {
  const url = getSkillExportUrl(name);
  const blob = await getDown(url);
  return blob;
}

// 获取Skill文件列表
export function listSkillFiles(name: string) {
  return get<{ data: string[] }>(`/api/v1/skills/${encodeURIComponent(name)}/files`);
}

// 列出产物
export function listArtifacts(sessionId: string, userId?: string) {
  const params = new URLSearchParams({ session_id: sessionId });
  if (userId) params.append('user_id', userId);
  return get<{ data: ArtifactMeta[] }>(`/api/v1/skills/artifacts?${params.toString()}`);
}

// 导出产物
export function getArtifactExportUrl(sessionId: string, filename: string, version?: number, userId?: string) {
  const params = new URLSearchParams({ session_id: sessionId, filename });
  if (userId) params.append('user_id', userId);
  if (version !== undefined) params.append('version', String(version));
  return `/api/v1/skills/artifacts/export?${params.toString()}`;
}

// 下载产物
export async function downloadArtifact(sessionId: string, filename: string, version?: number, userId?: string) {
  const url = getArtifactExportUrl(sessionId, filename, version, userId);
  const blob = await getDown(url);
  return blob;
}

// 删除产物
export function deleteArtifact(sessionId: string, filename: string, userId?: string) {
  const params = new URLSearchParams({ session_id: sessionId, filename });
  if (userId) params.append('user_id', userId);
  return del<{ message: string }>(`/api/v1/skills/artifacts?${params.toString()}`);
}