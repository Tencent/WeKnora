import { get } from "../../utils/request";

// Skill信息
export interface SkillInfo {
  name: string;
  description: string;
}

// 获取当前沙箱配置上可执行的 Skills；未传 sandboxConfigId 或
// skills_available 为 false 时，前端应隐藏/禁用 Skills 配置
export function listSkills(sandboxConfigId?: string) {
  return get<{ data: SkillInfo[]; skills_available?: boolean }>('/api/v1/skills', {
    params: sandboxConfigId ? { sandbox_config_id: sandboxConfigId } : {},
  });
}
