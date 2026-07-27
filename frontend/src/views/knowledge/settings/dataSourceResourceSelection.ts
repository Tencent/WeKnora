export function requiresResourceSelection(type: string): boolean {
  return type === 'dingtalk'
}

export function hasRequiredResourceSelection(type: string, selectedResourceIds: string[]): boolean {
  return !requiresResourceSelection(type) || selectedResourceIds.length > 0
}
