export interface KnowledgeUploadTarget {
  readonly kbId: string
  readonly kbName: string
}

/** Bind a global dialog's eventual result to the KB where it opened. */
export async function confirmKnowledgeUpload<Result>(
  target: KnowledgeUploadTarget,
  open: () => Promise<Result>,
  submit: (result: Result, target: KnowledgeUploadTarget) => void | Promise<void>,
): Promise<void> {
  if (!target.kbId) throw new Error('Missing upload knowledge base')
  const openedFor = Object.freeze({ kbId: target.kbId, kbName: target.kbName })
  const result = await open()
  await submit(result, openedFor)
}
