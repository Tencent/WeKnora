// Cross-component contract for the confirm-button flow. The card component
// dispatches, the chat view listens — the two must not import from each
// other (component → view would be a layering inversion), so the event name
// and detail shape live here. Tests pin the name as a literal on both sides
// on purpose (see A2uiCard.action.test.mjs).
export const A2UI_CONFIRM_EVENT = 'weknora:a2ui-confirm'

export interface A2uiConfirmDetail {
    status: 'started' | 'already_executed'
    sessionId: string
    messageId: string
    assistantMessageId: string
    /** The follow-up instruction the server sent to the agent. */
    text: string
}
