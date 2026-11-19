/**
 * Answers the assistant's page_action prompts: reads or fills the page beside
 * the assistant and posts the result back, without showing a card.
 */
import { resolveAgentPrompt } from '@/api/mcp-service'
import { PageFillError, type PageTextEdit } from './pageText'
import { activateTab, currentLocation, readFieldText, snapshotPage } from './pageSnapshot'
import { fillPage, undoFill } from './pageFill'
import { pageLinkLocation, type AssistantPageLink, type PageLinkLocation } from './assistantPageLink'
import { hasUnsavedChanges } from './unsavedChanges'

/** Moves the app to another page; the router in practice. */
export interface PageNavigator {
  /** The in-app path shown now, to come back to. */
  current: () => string
  /** Resolves to a failure, or nothing once the page is shown. */
  push: (location: PageLinkLocation) => Promise<unknown>
}

export interface PageActionPrompt {
  pending_id?: string
  tool_call_id?: string
  requested_at?: number
  timeout_seconds?: number
  request?: {
    action?: string
    ref?: string
    start_line?: number
    line_count?: number
    changes?: Array<{ ref: string; value?: unknown; edits?: PageTextEdit[] }>
    target?: Omit<AssistantPageLink, 'display_type' | 'label'>
  }
}

interface PageActionResponse {
  declined?: boolean
  error?: string
  location?: string
  snapshot?: string
  applied?: Array<{ ref: string; label: string; summary: string }>
  stayed?: boolean
  return_path?: string
}

// vue-router's NavigationFailureType.duplicated: already there, which is fine.
const NAVIGATION_DUPLICATED = 16
const SETTLE_QUIET_MS = 350
const SETTLE_MAX_MS = 4000

/** Waits until the page stops changing, or gives up and reads what is there. */
function settle(): Promise<void> {
  return new Promise(resolve => {
    let quiet = 0
    const done = () => {
      observer.disconnect()
      window.clearTimeout(quiet)
      window.clearTimeout(cap)
      resolve()
    }
    const observer = new MutationObserver(() => {
      window.clearTimeout(quiet)
      quiet = window.setTimeout(done, SETTLE_QUIET_MS)
    })
    observer.observe(document.body, { childList: true, subtree: true, characterData: true })
    quiet = window.setTimeout(done, SETTLE_QUIET_MS)
    const cap = window.setTimeout(done, SETTLE_MAX_MS)
  })
}

async function navigate(
  target: NonNullable<PageActionPrompt['request']>['target'], navigator?: PageNavigator,
): Promise<PageActionResponse> {
  const location = target && pageLinkLocation({ ...target, display_type: 'open_page', label: '' } as AssistantPageLink)
  if (!location || !navigator) return { error: `this page cannot be opened from here` }
  // Leaving would throw the user's edits away; they get the button instead.
  if (hasUnsavedChanges()) return { stayed: true }
  const returnPath = navigator.current()
  const failure = await navigator.push(location) as { type?: number } | undefined
  if (failure && failure.type !== NAVIGATION_DUPLICATED) return { stayed: true }
  await settle()
  return { ...snapshotPage(), return_path: returnPath }
}

const HANDLED_KEY = 'weknora:assistant-page-actions'
// A replayed stream can repeat a prompt long after it ended; the server
// rejects the late answer anyway, this only spares the page the work.
const STALE_AFTER_SECONDS = 600

function handledIds(): Set<string> {
  try {
    return new Set(JSON.parse(sessionStorage.getItem(HANDLED_KEY) || '[]'))
  } catch {
    return new Set()
  }
}

/** Claims a prompt for this tab, so a reload replaying the stream skips it. */
function claim(id: string): boolean {
  const ids = handledIds()
  if (ids.has(id)) return false
  ids.add(id)
  try {
    sessionStorage.setItem(HANDLED_KEY, JSON.stringify([...ids].slice(-200)))
  } catch {
    // Storage can be unavailable; the server still rejects a second answer.
  }
  return true
}

async function act(prompt: PageActionPrompt, key: string, navigator?: PageNavigator): Promise<PageActionResponse> {
  const request = prompt.request || {}
  try {
    if (request.action === 'navigate') return await navigate(request.target, navigator)
    if (request.action === 'read') {
      return request.ref
        ? readFieldText(request.ref, request.start_line || 1, request.line_count || 200)
        : snapshotPage()
    }
    if (request.action === 'select') {
      if (activateTab(request.ref || '')) await settle()
      return snapshotPage()
    }
    if (request.action === 'fill') {
      const applied = await fillPage(request.changes || [], key)
      return { location: currentLocation(), applied }
    }
    return { error: `unknown page action "${request.action}"` }
  } catch (err) {
    if (err instanceof PageFillError) return { error: err.message }
    console.warn('[Assistant page action]', err)
    return { error: 'the page could not be read or filled' }
  }
}

/**
 * enabled: the assistant sits beside a page and may use it. disabled: the
 * user turned page access off. unavailable: the chat fills the window, there
 * is no page beside it.
 */
export type PageAccessState = 'enabled' | 'disabled' | 'unavailable'

/** Handles one page_action prompt, without looking at the page unless enabled. */
export async function respondToPageAction(
  prompt: PageActionPrompt, access: PageAccessState, navigator?: PageNavigator,
): Promise<void> {
  const id = prompt.pending_id
  if (!id || !claim(id)) return
  const age = prompt.requested_at ? Date.now() / 1000 - prompt.requested_at : 0
  if (age > (prompt.timeout_seconds || 0) + STALE_AFTER_SECONDS) return
  const key = prompt.tool_call_id || id
  const response: PageActionResponse =
    access === 'enabled' ? await act(prompt, key, navigator)
      : access === 'disabled' ? { declined: true }
        : { error: 'the chat fills the window, there is no page open beside it' }
  try {
    await resolveAgentPrompt(id, 'page_action', response as Record<string, unknown>)
  } catch (err) {
    // The run no longer waits for this answer: it timed out or was answered
    // elsewhere. Take a fill back so the page matches what the model was told.
    if (response.applied?.length) await undoFill(key)
    console.warn('[Assistant page action] answer not accepted', err)
  }
}
