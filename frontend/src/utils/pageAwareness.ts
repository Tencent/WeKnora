/**
 * Follows what the user has in front of them beside the assistant: the
 * dialog or drawer open over the page, and the text they selected on it.
 */
import { currentView, editableTextField, isAssistantUI } from './pageSnapshot'
import { lineOf } from './pageText'

export interface PageSelectionInfo {
  text: string
  field?: string
  ref?: string
  startLine?: number
  endLine?: number
}

export interface PageAwarenessHandlers {
  onView: (view: string) => void
  onSelection: (selection: PageSelectionInfo | null) => void
}

const MAX_SELECTION = 4000

type TextControl = HTMLTextAreaElement | HTMLInputElement

let selectedField: WeakRef<TextControl> | null = null

function fieldHoldsSelection(): boolean {
  const control = selectedField?.deref()
  return !!control && control.isConnected && control.getClientRects().length > 0 &&
    (control.selectionEnd ?? 0) > (control.selectionStart ?? 0)
}

/** The field the current selection is in, if it is in one. */
export function selectedTextControl(): TextControl | null {
  return fieldHoldsSelection() ? selectedField!.deref()! : null
}

/** What is selected on the page now; undefined keeps the last selection. */
function readSelection(): PageSelectionInfo | null | undefined {
  const active = document.activeElement
  // Typing to the assistant moves the focus there; the page keeps its selection.
  if (active && active !== document.body && isAssistantUI(active)) return undefined
  if (active instanceof HTMLTextAreaElement || active instanceof HTMLInputElement) {
    const target = editableTextField(active)
    // A password or read-only field's selection is never picked up.
    if (!target) return fieldHoldsSelection() ? undefined : null
    const control = target.field.control as TextControl
    const start = control.selectionStart ?? 0
    const end = control.selectionEnd ?? 0
    const text = control.value.slice(start, end)
    if (end <= start || !text.trim()) {
      selectedField = null
      return null
    }
    selectedField = new WeakRef(control)
    const info: PageSelectionInfo = { text: text.slice(0, MAX_SELECTION), field: target.field.label, ref: target.ref }
    if (target.field.kind === 'multiline') {
      info.startLine = lineOf(control.value, start)
      info.endLine = lineOf(control.value, end - 1)
    }
    return info
  }
  const selection = document.getSelection()
  if (selection && !selection.isCollapsed && selection.rangeCount) {
    if (isAssistantUI(selection.anchorNode) || isAssistantUI(selection.focusNode)) return undefined
    const text = selection.toString().trim()
    if (text) {
      selectedField = null
      return { text: text.slice(0, MAX_SELECTION) }
    }
  }
  // A click elsewhere leaves a field's selection in the field, where the
  // assistant can still replace it.
  return fieldHoldsSelection() ? undefined : null
}

export function installPageAwareness(handlers: PageAwarenessHandlers): () => void {
  let lastView = ''
  let viewTimer = 0
  let selectionTimer = 0
  const checkView = () => {
    viewTimer = 0
    const view = currentView()
    if (view !== lastView) {
      lastView = view
      handlers.onView(view)
      // Closing a dialog takes the selection in its fields with it.
      if (selectedField && !fieldHoldsSelection()) {
        selectedField = null
        handlers.onSelection(null)
      }
    }
  }
  const scheduleView = () => {
    if (!viewTimer) viewTimer = window.setTimeout(checkView, 250)
  }
  const checkSelection = () => {
    selectionTimer = 0
    const selection = readSelection()
    if (selection !== undefined) handlers.onSelection(selection)
  }
  const scheduleSelection = () => {
    window.clearTimeout(selectionTimer)
    selectionTimer = window.setTimeout(checkSelection, 120)
  }
  // Text fields do not report selection changes the same way in every
  // browser, so the gestures that end a selection are watched too.
  const selectionEvents = ['selectionchange', 'select', 'pointerup', 'keyup'] as const
  selectionEvents.forEach(type => document.addEventListener(type, scheduleSelection, true))
  const observer = new MutationObserver(scheduleView)
  observer.observe(document.body, { childList: true, subtree: true, attributes: true, attributeFilter: ['class', 'style'] })
  checkView()
  return () => {
    selectionEvents.forEach(type => document.removeEventListener(type, scheduleSelection, true))
    observer.disconnect()
    window.clearTimeout(viewTimer)
    window.clearTimeout(selectionTimer)
  }
}
