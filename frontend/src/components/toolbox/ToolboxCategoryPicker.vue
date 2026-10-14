<template>
  <div class="toolbox-category-picker">
    <t-select :value="modelValue" multiple filterable clearable :min-collapsed-num="3" :disabled="disabled || creating"
      :options="categories.map(({ id, name }) => ({ label: name, value: id }))"
      :placeholder="$t('toolboxCategories.assignPlaceholder')"
      @change="emit('update:modelValue', $event as string[])" />
    <p class="toolbox-category-picker__hint">{{ $t('toolboxCategories.sharedHint') }}</p>
    <div v-if="showCreate || categories.length === 0" class="toolbox-category-picker__create">
      <t-input v-model="newName" :maxlength="64" :disabled="disabled || creating"
        :placeholder="$t('toolboxCategories.namePlaceholder')" @enter="createCategory" />
      <t-button :loading="creating" :disabled="disabled || !newName.trim()" @click="createCategory">
        {{ $t('toolboxCategories.create') }}
      </t-button>
    </div>
    <t-button v-else variant="text" :disabled="disabled" @click="showCreate = true">
      {{ $t('toolboxCategories.create') }}
    </t-button>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import { createToolboxCategory, type ToolboxCategory } from '@/api/toolbox-category'

const props = defineProps<{
  modelValue: string[]
  categories: ToolboxCategory[]
  disabled?: boolean
}>()
const emit = defineEmits<{
  (event: 'update:modelValue', value: string[]): void
  (event: 'created', category: ToolboxCategory): void
  (event: 'busy', value: boolean): void
}>()
const { t } = useI18n()
const showCreate = ref(false)
const newName = ref('')
const creating = ref(false)

async function createCategory() {
  const name = newName.value.trim()
  if (!name || creating.value || props.disabled) return
  creating.value = true
  emit('busy', true)
  try {
    const category = await createToolboxCategory(name)
    emit('created', category)
    emit('update:modelValue', [...props.modelValue, category.id])
    newName.value = ''
    showCreate.value = false
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('toolboxCategories.saveFailed'))
  } finally {
    creating.value = false
    emit('busy', false)
  }
}
</script>

<style scoped>
.toolbox-category-picker__hint {
  margin: 8px 0;
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-md);
}
.toolbox-category-picker__create { display: flex; gap: 8px; }
.toolbox-category-picker__create > :first-child { flex: 1; min-width: 0; }
</style>
