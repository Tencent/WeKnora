<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue'
import { A2UISurface, useMessageProcessor, type A2uiMessage } from 'a2ui-vue'
import { post } from '@/utils/request'
import { A2UI_CONFIRM_EVENT } from './a2uiEvents';

// A2uiCard is the per-message A2UI host. It is mounted into a placeholder div
// by mountA2uiSurfaces (utils/a2uiSurface.ts), each instance carrying its own
// nested Vue app + its own MessageProcessor (provideA2UI creates one per app),
// so surfaces from different messages never bleed into each other.
const props = defineProps<{
    frames: A2uiMessage[]
    sessionId: string
    messageId: string
}>()

const processor = useMessageProcessor()
// processMessages is synchronous: by the time this template first renders the
// surfaces already exist, so a one-shot read of getSurfaces() is complete.
processor.processMessages(props.frames)
const surfaces = Array.from(processor.getSurfaces().entries())

// Set when the server refuses the click. The card then falls back to showing
// its own frames as a code block rather than keeping live buttons that only
// fail. No new copy is introduced on purpose — the source fence is the message.
//
// Why a refusal can happen even though the model offered the button: the
// executor re-derives the button from the PERSISTED message by running its own
// fence regex over msg.Content, and that regex only tolerates `^ {0,3}` before
// the backticks. A fence the model nested in a blockquote ("> " prefix) or
// indented 4+ spaces inside a list item is still a code token for marked (so we
// render a card), but the server extracts nothing from it → 404 + a denied
// audit row. Silent dead buttons would be worse than showing where the card
// came from. Known limitation.
const reflowFailed = ref(false)
const framesJson = computed(() => JSON.stringify(props.frames, null, 2))

// Button click → the processor dispatches the official v0.9 action envelope
// ({ version:'v0.9', action:{ name, surfaceId, sourceComponentId, timestamp,
// context } }). We forward it to the server-side executor, which re-derives
// the action from the persisted component tree and refuses anything the model
// did not offer (route-B security model). The post() helper attaches the auth
// token + tenant header via the global axios interceptors.
// The path must keep the /api/v1 prefix: the axios instance's baseURL is
// getApiBaseUrl() = import.meta.env.BASE_URL, which is "/" in the Docker build
// and therefore contributes nothing. A bare "/agent/actions/execute" resolves
// at the SPA origin and nginx answers 405 for POST against the static root —
// no auth, no audit row, dead button (live-tested 2026-11-05).
const unsubscribe = processor.onEvent((evt) => {
    const a = evt.message.action
    void post('/api/v1/agent/actions/execute', {
        session_id: props.sessionId,
        message_id: props.messageId,
        surface_id: a.surfaceId,
        source_component_id: a.sourceComponentId,
        action_name: a.name,
        context: a.context ?? {},
    }).then((res) => {
        // The interceptor resolves to the response PAYLOAD, not the axios
        // response object; res?.data is the {status,...} object (?? res
        // tolerates a stub/post shape carrying the inner object directly).
        const data = res?.data ?? res;
        if (data && (data.status === 'started' || data.status === 'already_executed')) {
            window.dispatchEvent(new CustomEvent(A2UI_CONFIRM_EVENT, {
                detail: {
                    status: data.status,
                    sessionId: props.sessionId,
                    messageId: data.message_id ?? '',
                    assistantMessageId: data.assistant_message_id ?? '',
                    // The server echoes the PERSISTED context.message it sent
                    // to the agent — the single source of truth for the local
                    // user bubble; the envelope's own context is the fallback.
                    text: typeof data.message === 'string' ? data.message
                        : (typeof a.context?.message === 'string' ? a.context.message : ''),
                },
            }))
        }
    }).catch((err) => {
        // 409 = a run is already in progress in this session: expected while
        // the first click's run is still streaming — not a card failure.
        if (err?.status === 409) return
        console.error('[a2ui] action reflow failed', err)
        reflowFailed.value = true
    })
})
onBeforeUnmount(() => unsubscribe())
</script>

<template>
    <div v-if="reflowFailed" class="chat-a2ui-fallback">
        <pre><code>{{ framesJson }}</code></pre>
    </div>
    <template v-else>
        <A2UISurface
            v-for="[surfaceId, surface] in surfaces"
            :key="surfaceId"
            :surface-id="surfaceId"
            :surface="surface"
        />
    </template>
</template>
