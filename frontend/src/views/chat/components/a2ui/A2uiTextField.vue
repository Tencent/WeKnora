<script setup lang="ts">
// Our own TextField, overriding a2ui-vue's A2UITextField (see the override
// note in utils/a2uiCatalog.ts). Behaviour matches the official component:
// binding and validation go through useBinder, and writes use the same
// typeof-function guard on bound.value.setValue that the compiled official
// build uses. What it adds: a placeholder in the product's own language,
// longText rendered as a textarea, and every colour taken from a design
// token. Props are only allowed to carry variant (the override note says why).
import { computed } from 'vue'
import { useBinder, type VueComponentNode } from 'a2ui-vue'
import { a2uiTextFieldInputType } from '@/utils/a2uiCatalog'

const props = defineProps<{
    surfaceId: string | null
    component: VueComponentNode
    weight?: string | number
    variant?: unknown
}>()

const { bound } = useBinder({ surfaceId: props.surfaceId, component: props.component })

const label = computed(() => {
    const l = bound.value.label
    return typeof l === 'string' && l ? l : ''
})
const invalid = computed(() => bound.value.isValid === false)
const errors = computed<string[]>(() => bound.value.validationErrors ?? [])
const modelValue = computed(() => {
    const v = bound.value.value
    return typeof v === 'string' ? v : v == null ? '' : String(v)
})
// Only longText is multi-line in the official enum; number/obscured are carried by the input-type whitelist.
const isLongText = computed(() => props.variant === 'longText')
const inputType = computed(() => a2uiTextFieldInputType(props.variant))
// Same layout contract as A2uiButton.vue: the library weights its direct children with flex, so the host row carries an inline flex.
const hostStyle = computed(() => ({ flex: props.weight ?? 'initial' }))

function onInput(event: Event) {
    const target = event.target as HTMLInputElement | HTMLTextAreaElement
    if (!(target instanceof HTMLInputElement) && !(target instanceof HTMLTextAreaElement)) return
    const setter = bound.value.setValue
    typeof setter === 'function' && setter(target.value)
}
</script>

<template>
    <div class="a2ui-field" :class="{ 'is-invalid': invalid }" :style="hostStyle">
        <label v-if="label" class="a2ui-field__label">{{ label }}</label>
        <textarea
            v-if="isLongText"
            class="a2ui-field__input a2ui-field__textarea"
            :value="modelValue"
            rows="3"
            placeholder="请输入"
            @input="onInput"
        />
        <input
            v-else
            class="a2ui-field__input"
            :type="inputType"
            :value="modelValue"
            placeholder="请输入"
            @input="onInput"
        />
        <p v-if="errors.length" class="a2ui-field-error" role="alert">
            <span>{{ errors.join('，') }}</span>
        </p>
    </div>
</template>

<style scoped>
/* Token discipline is shared with A2uiButton.vue: radius, font size and motion come
//    from --app-*, size/border/background from --td-*, and never with a fallback
//    (:root:root already guarantees they resolve; a fallback would only hide a wrong
//    name). The 32px height has no --td-* height token, and height is outside the
//    styleGuard ratchet. */
.a2ui-field {
    display: block;
    min-height: 0;
}

.a2ui-field__label {
    display: block;
    margin-bottom: 4px;
    font-family: var(--app-font-family);
    font-size: var(--app-text-md);
    color: var(--td-text-color-secondary);
}

.a2ui-field__input {
    box-sizing: border-box;
    width: 100%;
    height: 32px;
    padding: 0 var(--td-comp-paddingLR-s);
    font-family: var(--app-font-family);
    font-size: var(--app-text-md);
    color: var(--td-text-color-primary);
    background-color: var(--td-bg-color-container);
    border: 1px solid var(--td-component-border);
    border-radius: var(--app-radius-md);
    transition: border-color var(--app-motion-fast) ease;
}

.a2ui-field__input::placeholder {
    color: var(--td-text-color-placeholder);
}

.a2ui-field__input:focus {
    outline: none;
    border-color: var(--td-brand-color);
}

.is-invalid .a2ui-field__input {
    border-color: var(--td-error-color);
}

.a2ui-field__textarea {
    height: auto;
    min-height: 72px;
    padding: var(--td-comp-paddingTB-s) var(--td-comp-paddingLR-s);
    resize: vertical;
}

/* Validation error text: the library's .a2ui-field-error carries its own data-v
//    hash and cannot reach here, so this file ships its own rule the way
//    A2uiButton.vue does (colour: --td-error-color). */
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
