import { del, get, post, put } from '@/utils/request'

export interface ToolboxCategory {
  id: string
  name: string
  created_at?: string
  updated_at?: string
}

export async function listToolboxCategories(): Promise<ToolboxCategory[]> {
  const response: any = await get('/api/v1/toolbox-categories')
  return response.data || []
}

export async function createToolboxCategory(name: string): Promise<ToolboxCategory> {
  const response: any = await post('/api/v1/toolbox-categories', { name })
  return response.data
}

export async function updateToolboxCategory(id: string, name: string): Promise<ToolboxCategory> {
  const response: any = await put(`/api/v1/toolbox-categories/${id}`, { name })
  return response.data
}

export async function deleteToolboxCategory(id: string): Promise<void> {
  await del(`/api/v1/toolbox-categories/${id}`)
}

async function replaceCategories(path: string, categoryIds: string[]): Promise<ToolboxCategory[]> {
  const response: any = await put(path, { category_ids: categoryIds })
  return response.data || []
}

export function replaceSkillCategories(skillId: string, categoryIds: string[]) {
  return replaceCategories(`/api/v1/skills/catalog/${skillId}/categories`, categoryIds)
}

export function replaceMCPServiceCategories(serviceId: string, categoryIds: string[]) {
  return replaceCategories(`/api/v1/mcp-services/${serviceId}/categories`, categoryIds)
}
