import { get } from "../../utils/request";

// Skill信息
export interface SkillInfo {
  name: string;
  description: string;
}

// 获取预装Skills列表
export function listSkills() {
  return get<{ data: SkillInfo[] }>('/api/v1/skills');
}
