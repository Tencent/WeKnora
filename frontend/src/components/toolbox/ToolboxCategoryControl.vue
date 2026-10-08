<template>
  <div class="toolbox-category-control">
    <t-select
      :value="modelValue"
      class="toolbox-category-control__select"
      :options="categoryOptions"
      :placeholder="$t('toolboxCategories.all')"
      clearable
      @change="emit('update:modelValue', String($event || ''))"
      @clear="emit('update:modelValue', '')"
    />
    <t-button v-if="canManage" variant="outline" @click="managerVisible = true">
      <template #icon><t-icon name="setting" /></template>
      {{ $t('toolboxCategories.manage') }}
    </t-button>
  </div>

  <t-dialog
    v-model:visible="managerVisible"
    :header="$t('toolboxCategories.manageTitle')"
    :footer="false"
    width="520px"
  >
    <div class="toolbox-category-manager">
      <p>{{ $t('toolboxCategories.sharedHint') }}</p>
      <div class="toolbox-category-manager__create">
        <t-input
          v-model="newName"
          :placeholder="$t('toolboxCategories.namePlaceholder')"
          :maxlength="64"
          @enter="createCategory"
        />
        <t-button theme="primary" :loading="creating" :disabled="!newName.trim()" @click="createCategory">
          {{ $t('common.add') }}
        </t-button>
      </div>
      <t-empty v-if="categories.length === 0" :description="$t('toolboxCategories.empty')" />
      <ul v-else class="toolbox-category-manager__list">
        <li v-for="category in categories" :key="category.id" class="toolbox-category-manager__row">
          <t-input
            v-if="editingId === category.id"
            v-model="editingName"
            :maxlength="64"
            autofocus
            @enter="saveCategory(category.id)"
          />
          <span v-else class="toolbox-category-manager__name">{{ category.name }}</span>
          <div class="toolbox-category-manager__actions">
            <template v-if="editingId === category.id">
              <t-button size="small" theme="primary" :loading="busyId === category.id" @click="saveCategory(category.id)">
                {{ $t('common.save') }}
              </t-button>
              <t-button size="small" variant="text" @click="cancelEdit">{{ $t('common.cancel') }}</t-button>
            </template>
            <template v-else>
              <t-button size="small" variant="text" @click="beginEdit(category)">{{ $t('common.edit') }}</t-button>
              <t-popconfirm :content="$t('toolboxCategories.deleteConfirm', { name: category.name })"
                @confirm="removeCategory(category.id)">
                <t-button size="small" theme="danger" variant="text" :loading="busyId === category.id">
                  {{ $t('common.delete') }}
                </t-button>
              </t-popconfirm>
            </template>
          </div>
        </li>
      </ul>
    </div>
  </t-dialog>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import {
  createToolboxCategory,
  deleteToolboxCategory,
  updateToolboxCategory,
  type ToolboxCategory,
} from '@/api/toolbox-category'

const props = defineProps<{
  modelValue: string
  categories: ToolboxCategory[]
  canManage: boolean
}>()

const emit = defineEmits<{
  (event: 'update:modelValue', value: string): void
  (event: 'changed'): void
}>()

const { t } = useI18n()
const managerVisible = ref(false)
const newName = ref('')
const creating = ref(false)
const editingId = ref('')
const editingName = ref('')
const busyId = ref('')
const categoryOptions = computed(() => props.categories.map(({ id, name }) => ({ label: name, value: id })))

async function createCategory() {
  const name = newName.value.trim()
  if (!name || creating.value) return
  creating.value = true
  try {
    await createToolboxCategory(name)
    newName.value = ''
    emit('changed')
    MessagePlugin.success(t('toolboxCategories.created'))
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('toolboxCategories.saveFailed'))
  } finally {
    creating.value = false
  }
}

function beginEdit(category: ToolboxCategory) {
  editingId.value = category.id
  editingName.value = category.name
}

function cancelEdit() {
  editingId.value = ''
  editingName.value = ''
}

async function saveCategory(id: string) {
  const name = editingName.value.trim()
  if (!name || busyId.value) return
  busyId.value = id
  try {
    await updateToolboxCategory(id, name)
    cancelEdit()
    emit('changed')
    MessagePlugin.success(t('toolboxCategories.updated'))
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('toolboxCategories.saveFailed'))
  } finally {
    busyId.value = ''
  }
}

async function removeCategory(id: string) {
  if (busyId.value) return
  busyId.value = id
  try {
    await deleteToolboxCategory(id)
    if (props.modelValue === id) emit('update:modelValue', '')
    emit('changed')
    MessagePlugin.success(t('toolboxCategories.deleted'))
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('toolboxCategories.deleteFailed'))
  } finally {
    busyId.value = ''
  }
}
</script>

<style scoped lang="less">
.toolbox-category-control,
.toolbox-category-manager__create,
.toolbox-category-manager__row,
.toolbox-category-manager__actions {
  display: flex;
  align-items: center;
  gap: 8px;
}

.toolbox-category-control__select {
  min-width: 180px;
}

.toolbox-category-manager {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.toolbox-category-manager__create > :first-child,
.toolbox-category-manager__name,
.toolbox-category-manager__row > :first-child {
  flex: 1;
  min-width: 0;
}

.toolbox-category-manager__list {
  display: flex;
  flex-direction: column;
  gap: 8px;
  max-height: 360px;
  margin: 0;
  padding: 0;
  overflow: auto;
  list-style: none;
}

.toolbox-category-manager__row {
  min-height: 40px;
  padding: 6px 8px;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-sm);
}

.toolbox-category-manager__name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
