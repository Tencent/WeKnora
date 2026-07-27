<script setup lang="ts">
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';


const props = withDefaults(defineProps<{
  visible: boolean;
  // Display name of the target folder (root label is never used here - root
  // is not deletable from the folder action menu).
  folderName: string;
  // Descendant folder count (excluding the folder itself). Always known -
  // computed by the page from the tree index via descendantIds().
  descendantFolderCount: number;
  // Recursive document count for the folder (nullable - unknown when the tree
  // is not loaded, since only the tree carries authoritative recursive counts).
  documentCount: number | null;
  submitting?: boolean;
}>(), {
  submitting: false,
});

const emit = defineEmits<{
  (e: 'update:visible', visible: boolean): void;
  (e: 'confirm'): void;
  (e: 'cancel'): void;
}>();

const { t } = useI18n();

// Empty = no descendant folders and (known) zero documents. When the document
// count is unknown we cannot promise "empty", so treat it as recursive to be
// safe (the impact line then shows the folder-only count).
const isEmpty = computed(
  () =>
    props.descendantFolderCount === 0 &&
    (props.documentCount === 0),
);

// Whether to show the full impact line (both counts) vs the folder-only line.
// Shown when the operation is recursive and documents are known.
const showDocCount = computed(
  () => props.documentCount !== null && props.documentCount > 0,
);

function onConfirm() {
  if (props.submitting) return;
  emit('confirm');
}

// t-dialog fires `update:visible(false)` for any dismiss (cancel button,
// overlay, esc). We forward it and also emit `cancel` so parents that key off
// the cancel signal still work. `confirm` is NOT auto-closed - the parent
// closes the dialog after the async batch-delete submit resolves, so the
// dialog stays open on error.
function onUpdateVisible(val: boolean) {
  emit('update:visible', val);
  if (!val) emit('cancel');
}
</script>

<template>
  <t-dialog
    :visible="visible"
    :header="t('knowledgeBase.folderDeleteTitle')"
    :confirm-btn="{
      content: t('knowledgeBase.confirmDelete'),
      theme: 'danger',
      loading: submitting,
      disabled: submitting,
    }"
    :cancel-btn="{ content: t('common.cancel'), disabled: submitting }"
    width="440px"
    destroy-on-close
    :close-on-overlay-click="!submitting"
    :close-on-esc-keydown="!submitting"
    @confirm="onConfirm"
    @update:visible="onUpdateVisible"
  >
    <div class="folder-delete">
      <!-- Warning icon + confirmation line -->
      <div class="folder-delete-head">
        <t-icon name="error-circle" size="20px" class="folder-delete-icon" aria-hidden="true" />
        <p class="folder-delete-confirm">
          <template v-if="isEmpty">
            {{ t('knowledgeBase.folderDeleteEmptyConfirm', { name: folderName }) }}
          </template>
          <template v-else>
            {{ t('knowledgeBase.folderDeleteRecursiveConfirm', { name: folderName }) }}
          </template>
        </p>
      </div>

      <!-- Recursive impact line: descendant folders + documents (when known). -->
      <div
        v-if="!isEmpty && (descendantFolderCount > 0 || showDocCount)"
        class="folder-delete-impact"
      >
        <template v-if="showDocCount">
          {{
            t('knowledgeBase.folderDeleteImpact', {
              folderCount: descendantFolderCount,
              docCount: documentCount,
            })
          }}
        </template>
        <template v-else>
          {{
            t('knowledgeBase.folderDeleteImpactFolders', {
              folderCount: descendantFolderCount,
            })
          }}
        </template>
      </div>

      <!-- Async note: submitted, not done. MUST NOT promise completion. -->
      <div class="folder-delete-note">
        <t-icon name="info-circle" size="14px" class="folder-delete-note-icon" aria-hidden="true" />
        <span>{{ t('knowledgeBase.folderDeleteAsyncNote') }}</span>
      </div>
    </div>
  </t-dialog>
</template>

<style scoped lang="less">
.folder-delete {
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 4px 0 0;
}

.folder-delete-head {
  display: flex;
  align-items: flex-start;
  gap: 10px;
}

// error color for danger semantics.
.folder-delete-icon {
  flex-shrink: 0;
  color: var(--td-error-color-6);
  margin-top: 1px;
}

.folder-delete-confirm {
  margin: 0;
  font-size: 14px;
  line-height: 22px;
  color: var(--td-text-color-primary);
  word-break: break-word;
}

.folder-delete-impact {
  padding: 8px 12px;
  margin-left: 30px; // align under the confirm text, past the icon
  background: var(--td-error-color-1);
  border-radius: 6px;
  font-size: 13px;
  line-height: 20px;
  color: var(--td-text-color-secondary);
  font-variant-numeric: tabular-nums;
}

.folder-delete-note {
  display: flex;
  align-items: flex-start;
  gap: 6px;
  margin-left: 30px;
  font-size: 12px;
  line-height: 18px;
  color: var(--td-text-color-placeholder);
}

.folder-delete-note-icon {
  flex-shrink: 0;
  color: var(--td-text-color-placeholder);
  margin-top: 1px;
}
</style>
