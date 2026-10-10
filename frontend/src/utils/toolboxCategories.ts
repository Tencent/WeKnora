import type { ToolboxCategory } from '@/api/toolbox-category'

export function countResourcesByCategory(
  resources: Array<{ categories?: ToolboxCategory[] }>,
): Record<string, number> {
  const counts: Record<string, number> = { '': resources.length }
  for (const resource of resources) {
    for (const category of resource.categories || []) {
      counts[category.id] = (counts[category.id] || 0) + 1
    }
  }
  return counts
}

export function hasToolboxCategory(
  categories: ToolboxCategory[] | undefined,
  selectedCategoryId: string,
): boolean {
  return !selectedCategoryId || Boolean(categories?.some((category) => category.id === selectedCategoryId))
}
