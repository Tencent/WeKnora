/**
 * Drag-and-drop contract between the document list and the folder tree.
 *
 * Custom MIME types (rather than text/plain) let a drop target inspect
 * `dataTransfer.types` and reject payloads it does not understand, instead of
 * parsing an arbitrary string and guessing. They also keep documents dropped
 * from outside the app — files from the desktop, links from another tab —
 * from being mistaken for an internal move.
 */
export const KB_DOC_DRAG_TYPE = 'application/x-weknora-knowledge-ids'
export const KB_FOLDER_DRAG_TYPE = 'application/x-weknora-folder-id'

/**
 * Writes the payload for dragging documents onto a folder.
 *
 * Dragging a row that is part of the current selection moves the whole
 * selection; dragging an unselected row moves just that row. This mirrors how
 * file managers behave and avoids the surprise of a multi-select being
 * silently reduced to one item.
 */
export function setDocumentDragPayload(
  event: DragEvent,
  itemId: string,
  selectedIds: Set<string>,
  label?: string,
): string[] {
  const ids = selectedIds.has(itemId) ? Array.from(selectedIds) : [itemId]
  const dt = event.dataTransfer
  if (dt) {
    dt.setData(KB_DOC_DRAG_TYPE, JSON.stringify(ids))
    // Some browsers cancel a drag that carries no text/plain payload.
    dt.setData('text/plain', label || itemId)
    dt.effectAllowed = 'move'
  }
  return ids
}
