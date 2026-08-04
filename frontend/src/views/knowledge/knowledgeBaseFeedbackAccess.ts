export function canAccessChunkFeedbackSettings(
  mode: 'create' | 'edit',
  knowledgeBaseId: string | undefined,
  isAdmin: boolean,
  isCurrentTenantKnowledgeBase: boolean,
): boolean {
  return mode === 'edit' && Boolean(knowledgeBaseId) && isAdmin && isCurrentTenantKnowledgeBase
}
