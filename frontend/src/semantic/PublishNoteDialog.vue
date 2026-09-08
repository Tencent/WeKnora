<template>
  <t-dialog
    v-model:visible="visible"
    :header="t('semantic.model.publish')"
    width="460px"
    :confirm-btn="{ content: t('semantic.model.publish'), theme: 'primary', loading: loading }"
    @confirm="confirm"
  >
    <t-form label-width="0">
      <t-form-item :label="t('semantic.model.publishNote')" label-align="top">
        <t-textarea
          v-model="note"
          :autosize="{ minRows: 3, maxRows: 5 }"
          :placeholder="t('semantic.model.publishNotePh')"
          maxlength="200"
        />
      </t-form-item>
    </t-form>
  </t-dialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

const props = defineProps<{ visible: boolean; loading?: boolean }>()
const emit = defineEmits<{ (e: 'update:visible', v: boolean): void; (e: 'confirm', note: string): void }>()

const { t } = useI18n()
const note = ref('')

const visible = computed({
  get: () => props.visible,
  set: v => emit('update:visible', v)
})

watch(
  () => props.visible,
  v => {
    if (v) note.value = ''
  }
)

function confirm() {
  emit('confirm', note.value)
}
</script>
