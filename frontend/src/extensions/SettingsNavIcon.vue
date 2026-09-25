<template>
  <!-- 网络搜索：地球 -->
  <svg v-if="icon.kind === 'builtin' && icon.name === 'globe'" width="17" height="17" viewBox="0 0 18 18" fill="none"
    xmlns="http://www.w3.org/2000/svg" class="nav-icon">
    <circle cx="9" cy="9" r="7" stroke="currentColor" stroke-width="1.2" fill="none" />
    <path d="M 9 2 A 3.5 7 0 0 0 9 16" stroke="currentColor" stroke-width="1.2" fill="none" />
    <path d="M 9 2 A 3.5 7 0 0 1 9 16" stroke="currentColor" stroke-width="1.2" fill="none" />
    <line x1="2.94" y1="5.5" x2="15.06" y2="5.5" stroke="currentColor" stroke-width="1.2" stroke-linecap="round" />
    <line x1="2.94" y1="12.5" x2="15.06" y2="12.5" stroke="currentColor" stroke-width="1.2" stroke-linecap="round" />
  </svg>
  <!-- WeKnora Cloud：W -->
  <svg v-else-if="icon.kind === 'builtin' && icon.name === 'weknora'" width="17" height="17" viewBox="0 0 18 18"
    fill="none" xmlns="http://www.w3.org/2000/svg" class="nav-icon">
    <rect x="1.5" y="1.5" width="15" height="15" rx="3.5" stroke="currentColor" stroke-width="1.2" fill="none" />
    <path d="M4.5 5.5L6.5 12.5L9 7.5L11.5 12.5L13.5 5.5" stroke="currentColor" stroke-width="1.3"
      stroke-linecap="round" stroke-linejoin="round" fill="none" />
  </svg>
  <!-- 沙箱：隔离运行窗口 -->
  <svg v-else-if="icon.kind === 'builtin' && icon.name === 'sandbox'" width="17" height="17" viewBox="0 0 18 18"
    fill="none" xmlns="http://www.w3.org/2000/svg" class="nav-icon">
    <rect x="2.5" y="3" width="13" height="12" rx="2" stroke="currentColor" stroke-width="1.2" fill="none" />
    <path d="M2.5 6.5h13" stroke="currentColor" stroke-width="1.2" />
    <path d="M5.5 10h4M5.5 12.5h2.5" stroke="currentColor" stroke-width="1.2" stroke-linecap="round" />
  </svg>
  <span v-else-if="icon.kind === 'emoji'" class="nav-icon nav-icon-emoji">{{ icon.value }}</span>
  <t-icon v-else :name="icon.kind === 'tdesign' ? icon.name : 'setting'" class="nav-icon" />
</template>

<script setup lang="ts">
import type { SettingsNavIcon } from './settingsSections'

// Renders a settings section's sidebar icon. Styling comes from the
// .nav-icon rules of the settings modal.
defineProps<{ icon: SettingsNavIcon }>()
</script>
