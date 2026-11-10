<script setup lang="ts">
import { computed } from 'vue'
import { A2UiRenderer, useBinder, type VueComponentNode } from 'a2ui-vue'
import { a2uiButtonClass } from '@/utils/a2uiCatalog'

// Our own Button (replaces a2ui-vue's A2UIButton; see the override point in utils/a2uiCatalog.ts).
// Behaviour matches the official one: it fires bound.action, disables itself when validation fails,
// puts the error text right after the button, and takes the label from another Text component via child.
// Three things more than the official one:
//   1. variant really takes effect (the official renderer never reads it: in the compiled output it only appears in the props declaration);
//   2. <button type="button"> — the official one has no type, so inside a <form> it would also submit the form;
//   3. colours reference the --td-* tokens directly: the library button uses its own absolute p/n colour
//      scale (color-bgc-p30 = the primary mixed with 40% black, a different value from the app's
//      --td-brand-color), so matching the app's other buttons needs a direct reference to --td-*.
const props = defineProps<{
    surfaceId: string | null
    component: VueComponentNode
    weight?: string | number
    action?: unknown
    variant?: unknown
}>()

const { bound } = useBinder({ surfaceId: props.surfaceId, component: props.component })

// Same as the official one: the label node lives on properties.child (not children, so resolveChildren is not used).
const child = computed(() => props.component?.properties?.child ?? null)
const invalid = computed(() => bound.value.isValid === false)
const errors = computed<string[]>(() => bound.value.validationErrors ?? [])
// The official renderer turns weight into --v92672cf6 via useCssVars, and its scoped rule reads that as flex;
// this component has no such data-v hash, so the flex is written inline directly, with the same "initial" default.
const hostStyle = computed(() => ({ flex: props.weight ?? 'initial' }))

function onClick() {
    if (invalid.value) return
    const action = bound.value.action
    if (typeof action === 'function') (action as () => void)()
}
</script>

<template>
    <!-- The host is a <div>, not the library's <a2ui-button>: an SFC resolves itself by file name into
         <a2ui-button> (implicit self-reference), so copying that tag makes this component recursively render
         itself — which is what vue-tsc reports. Swapping the tag costs nothing: the library's a2ui-button
         rule carries its own data-v hash (it never reached here), a2ui-text is the only unscoped bare-tag rule. -->
    <div class="a2ui-btn-host" :style="hostStyle">
        <button
            type="button"
            :class="a2uiButtonClass(variant)"
            :disabled="invalid"
            :aria-invalid="invalid"
            @click="onClick"
        >
            <A2UiRenderer v-if="child" :surface-id="surfaceId" :component="child" />
        </button>
        <p v-if="errors.length" class="a2ui-field-error" role="alert">
            <span>{{ errors.join('，') }}</span>
        </p>
    </div>
</template>

<style scoped>
/* Modelled on the .btn of the reference confirm-card mock (33px high / 8px radius / 13px text / 500 weight),
   with only its blue primary swapped for the brand green (--td-brand-color).
   font-family must be set here: the UA default for <button> is the system button font, which does not
   inherit from the app, so without it the label renders in a different face than the body text beside it.
   Token choices follow the ratchet in assets/theme/styleGuard.test.mjs: radius / font size / duration always
   use our own --app-* (--app-radius-md=8, --app-text-md=13, --app-motion-fast=150ms);
   --td-* only for component sizes that only TDesign defines, and never with a fallback — that rule's baseline is 0,
   the :root:root rule at the top of assets/theme/theme.css guarantees --td-* always exists, and a
   fallback would only hide a misspelled name.
   33px has no token (the app's medium is 32px); height is not in the ratchet, so it copies the reference card. */
.a2ui-btn-host {
    display: block;
    min-height: 0;
}

/* Proportions of the reference card's .btn: 33px high / 8px radius / 13px text / 500 weight / 6px icon gap.
   33px has no token (the app's medium is 32px), but height is not in the ratchet, so it copies the reference card. */
.a2ui-btn {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    gap: 6px;
    box-sizing: border-box;
    height: 33px;
    padding: 0 15px;
    border: 1px solid transparent;
    border-radius: var(--app-radius-md);
    font-family: var(--app-font-family);
    font-size: var(--app-text-md);
    font-weight: 500;
    line-height: 1;
    text-transform: none;
    white-space: nowrap;
    cursor: pointer;
    transition: background-color var(--app-motion-fast) ease, border-color var(--app-motion-fast) ease,
        color var(--app-motion-fast) ease;
}

/* The label is another component (Text's class map ships margin-bottom: 8px); unless that is cleared the text
   gets pushed too high, and only :deep() reaches into a child component's DOM. */
.a2ui-btn :deep(*) {
    margin: 0;
}

.a2ui-btn:focus-visible {
    outline: 2px solid var(--td-brand-color);
    outline-offset: 2px;
}

.a2ui-btn:disabled {
    cursor: not-allowed;
    opacity: 0.65;
}

.a2ui-btn--primary {
    background-color: var(--td-brand-color);
    border-color: var(--td-brand-color);
    /* The primary label is **pinned to white**: an explicit requirement (white in both light and dark mode),
       so the theme-flipping "anti-color" text token is deliberately **not** used here — in dark mode it would
       flip to a dark colour and look muddy on the brand green. White on green (light #07c05f / dark #06b04d,
       both mid-tones) contrasts enough either way; do not switch it back to a --td text token — that would undo this pin. */
    color: #fff;
    /* The raised shadow of the reference card's .btn-primary. The green is derived from the brand colour with
       color-mix at 26% opacity instead of a bare rgba(7,192,95,…): styleGuard's brand-rgba rule (baseline 0)
       would flag it, and a literal would not follow the brand colour as it darkens in dark mode. */
    box-shadow: 0 2px 8px color-mix(in srgb, var(--td-brand-color) 26%, transparent);
}

/* The label is rendered by a child component (a2ui-text) that carries the library's own colour class, so
   inheritance may not win; :deep pins white onto it so "the primary label is always white" is not overridden. */
.a2ui-btn--primary :deep(*) {
    color: #fff;
}

.a2ui-btn--primary:hover:not(:disabled) {
    background-color: var(--td-brand-color-hover);
    border-color: var(--td-brand-color-hover);
}

.a2ui-btn--primary:active:not(:disabled) {
    background-color: var(--td-brand-color-active);
    border-color: var(--td-brand-color-active);
}

/* default matches the reference card's .btn: white background + border (it used to be the light grey
   secondarycontainer; only white blends into the white card nested inside the card). */
.a2ui-btn--default {
    background-color: var(--td-bg-color-container);
    border-color: var(--td-component-border);
    color: var(--td-text-color-primary);
}

.a2ui-btn--default:hover:not(:disabled) {
    background-color: var(--td-bg-color-component-hover);
}

.a2ui-btn--default:active:not(:disabled) {
    background-color: var(--td-bg-color-component-active);
}

/* borderless: the protocol means "like a link" — no border, no background. */
.a2ui-btn--borderless {
    background-color: transparent;
    border-color: transparent;
    color: var(--td-text-color-brand);
    padding: 0 var(--td-comp-paddingLR-s);
}

.a2ui-btn--borderless:hover:not(:disabled) {
    color: var(--td-brand-color-hover);
}

/* Validation error text: the library's .a2ui-field-error rule carries its own data-v hash and cannot reach
   this component, so font size and colour must be given here (--td-error-color, not the library's hard-coded #d92d20). */
.a2ui-field-error {
    display: flex;
    align-items: flex-start;
    gap: 4px;
    margin: 6px 0 0;
    color: var(--td-error-color);
    font-size: var(--app-text-sm);
    font-weight: 500;
}
</style>
