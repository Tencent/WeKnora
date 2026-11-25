<template>
  <svg class="chat-action-icon" width="18" height="18" viewBox="0 0 24 24" fill="none"
    stroke="currentColor" stroke-width="1.65" stroke-linecap="round" stroke-linejoin="round"
    aria-hidden="true" focusable="false">
    <template v-if="name === 'copy'">
      <rect x="4" y="8" width="12" height="13" rx="3" />
      <path d="M9 8V6a3 3 0 0 1 3-3h6a3 3 0 0 1 3 3v7a3 3 0 0 1-3 3h-2" />
    </template>
    <template v-else-if="name === 'git-branch'">
      <circle cx="6" cy="5" r="2" />
      <circle cx="6" cy="19" r="2" />
      <circle cx="18" cy="5" r="2" />
      <path d="M6 7v10m12-10a8 8 0 0 1-8 8H6" />
    </template>
    <template v-else-if="name === 'rollback'">
      <path d="m8 4-5 5 5 5M3 9h11a7 7 0 0 1 0 14" transform="translate(0 -2)" />
    </template>
    <template v-else-if="name === 'bookmark-add'">
      <path d="M19 21l-7-4-7 4V5a2 2 0 0 1 2-2h10a2 2 0 0 1 2 2v16Z" />
      <path d="M9 9h6m-3-3v6" />
    </template>
    <template v-else-if="name === 'folder'">
      <path d="M3 7V6a2 2 0 0 1 2-2h4l2 3h8a2 2 0 0 1 2 2v9a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V7Z" />
    </template>
    <template v-else-if="name === 'info-circle'">
      <circle cx="12" cy="12" r="9" />
      <path d="M12 11v6m0-10v.01" />
    </template>
    <template v-else-if="name === 'play'">
      <circle cx="12" cy="12" r="9" />
      <path d="m10 8 6 4-6 4V8Z" />
    </template>
    <template v-else-if="name === 'resume'">
      <path d="M3 10a9 9 0 1 1 2 8M3 4v6h6" />
      <path d="M12 7v5l3 2" />
    </template>
    <template v-else-if="name === 'pause'">
      <circle cx="12" cy="12" r="9" />
      <path d="M9 8v8m6-8v8" />
    </template>
    <template v-else-if="name === 'edit'">
      <path d="m15 4 5 5M4 20l5-1L20 8a2.1 2.1 0 0 0-5-5L4 14l-1 7Z" />
    </template>
    <template v-else-if="name === 'delete'">
      <path d="M3 6h18M9 6V3h6v3M5 6l1 14a1 1 0 0 0 1 1h10a1 1 0 0 0 1-1l1-14M10 10v7m4-7v7" />
    </template>
  </svg>
</template>

<script setup lang="ts">
defineProps<{
  name: 'copy' | 'git-branch' | 'rollback' | 'bookmark-add' | 'folder' | 'info-circle' | 'play' | 'resume' | 'pause' | 'edit' | 'delete'
}>()
</script>
