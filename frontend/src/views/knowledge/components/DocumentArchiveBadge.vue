<script setup lang="ts">
import { useI18n } from 'vue-i18n';

const { t } = useI18n();
</script>

<template>
  <!--
    Neutral on purpose: an archived file is not a failure and not a draft, it is a
    deliberately unindexed record. The grey default theme keeps it from reading as
    an error, and the tooltip states the retrieval consequence instead of leaving
    the user to wonder why the document never shows up in answers.
  -->
  <t-tag
    class="document-archive-badge"
    size="small"
    theme="default"
    variant="light"
    :title="t('knowledgeBase.storedHint')"
  >
    {{ t('knowledgeBase.statusStored') }}
  </t-tag>
</template>
