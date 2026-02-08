<template>
  <div
    class="space-avatar"
    :style="avatarStyle"
    :class="{ 'space-avatar-small': size === 'small', 'space-avatar-large': size === 'large', 'space-avatar-emoji': isEmoji }"
  >
    <template v-if="isEmoji">
      <span class="space-avatar-emoji-char">{{ emojiChar }}</span>
    </template>
    <template v-else>
      <svg class="space-avatar-decoration" viewBox="0 0 56 40" preserveAspectRatio="xMaxYMax meet" fill="none" xmlns="http://www.w3.org/2000/svg" aria-hidden="true">
        <circle cx="10" cy="12" r="4" stroke="currentColor" stroke-width="1.5" fill="none" opacity="0.5"/>
        <circle cx="28" cy="8" r="5" stroke="currentColor" stroke-width="1.8" fill="none" opacity="0.7"/>
        <circle cx="46" cy="14" r="4" stroke="currentColor" stroke-width="1.5" fill="none" opacity="0.5"/>
        <path d="M14 13 L24 10 M32 10 L42 13" stroke="currentColor" stroke-width="1.2" stroke-linecap="round" opacity="0.4"/>
        <circle cx="28" cy="28" r="6" stroke="currentColor" stroke-width="1.2" fill="none" opacity="0.35"/>
        <path d="M28 14 L28 22 M20 18 L26 24 M36 18 L30 24" stroke="currentColor" stroke-width="1" stroke-linecap="round" opacity="0.3"/>
      </svg>
      <span class="space-avatar-letter" :style="letterStyle">{{ letter }}</span>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue';

const props = withDefaults(defineProps<{
  name: string;
  /** Optional: "emoji:🚀" for emoji avatar; otherwise name-based */
  avatar?: string;
  size?: 'small' | 'medium' | 'large';
}>(), {
  size: 'medium',
  avatar: ''
});

const isEmoji = computed(() => {
  const v = (props.avatar || '').trim();
  return v.startsWith('emoji:') && v.length > 6;
});

const emojiChar = computed(() => {
  const v = (props.avatar || '').trim();
  if (!v.startsWith('emoji:')) return '';
  return v.slice(6).trim() || '';
});

// 预定义渐变色（与项目绿色主色协调，偏空间/协作感）
const gradients = [
  { from: '#07c05f', to: '#059669' },  // 主绿
  { from: '#11998e', to: '#38ef7d' },  // 深绿渐变
  { from: '#43e97b', to: '#38f9d7' },  // 绿青
  { from: '#02aab0', to: '#00cdac' },  // 青绿
  { from: '#36d1dc', to: '#5b86e5' }, // 青蓝
  { from: '#4facfe', to: '#00f2fe' },  // 蓝青
  { from: '#667eea', to: '#764ba2' },  // 紫蓝
  { from: '#4776e6', to: '#8e54e9' },  // 蓝紫
  { from: '#56ab2f', to: '#a8e063' },  // 草绿
  { from: '#00b09b', to: '#96c93d' },  // 青绿
  { from: '#5ee7df', to: '#b490ca' },  // 青紫
  { from: '#614385', to: '#516395' },  // 深紫蓝
];

const hashCode = (str: string): number => {
  let hash = 0;
  for (let i = 0; i < str.length; i++) {
    const char = str.charCodeAt(i);
    hash = ((hash << 5) - hash) + char;
    hash = hash & hash;
  }
  return Math.abs(hash);
};

const letter = computed(() => {
  const name = props.name?.trim() || '';
  if (!name) return '?';
  const firstChar = name.charAt(0);
  if (/[a-zA-Z]/.test(firstChar)) return firstChar.toUpperCase();
  return firstChar;
});

const gradient = computed(() => {
  const hash = hashCode(props.name || '');
  return gradients[hash % gradients.length];
});

const avatarStyle = computed(() => {
  if (isEmoji.value) {
    return { background: 'linear-gradient(135deg, #f1f5f9 0%, #e2e8f0 100%)' };
  }
  const g = gradient.value;
  return {
    background: `linear-gradient(135deg, ${g.from} 0%, ${g.to} 100%)`
  };
});

const letterStyle = computed(() => {
  const g = gradient.value;
  return {
    textShadow: `0 1px 2px ${g.to}80, 0 0 8px ${g.from}30`
  };
});
</script>

<style scoped lang="less">
.space-avatar {
  position: relative;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 32px;
  height: 32px;
  border-radius: 8px;
  flex-shrink: 0;
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.1);
  overflow: hidden;

  &.space-avatar-small {
    width: 22px;
    height: 22px;
    border-radius: 5px;
    box-shadow: none;

    .space-avatar-letter {
      font-size: 11px;
    }

    .space-avatar-decoration {
      display: none;
    }
  }

  &.space-avatar-large {
    width: 48px;
    height: 48px;
    border-radius: 12px;

    .space-avatar-letter {
      font-size: 20px;
    }

    .space-avatar-emoji-char {
      font-size: 28px;
    }
  }

  &.space-avatar-emoji {
    .space-avatar-emoji-char {
      position: relative;
      z-index: 1;
      line-height: 1;
      user-select: none;
    }
  }
}

.space-avatar-emoji-char {
  font-size: 18px;
  line-height: 1;

  .space-avatar-small & {
    font-size: 14px;
  }
}

.space-avatar-decoration {
  position: absolute;
  right: 0;
  bottom: 0;
  width: 55%;
  height: 55%;
  opacity: 0.35;
  color: rgba(255, 255, 255, 0.9);
  pointer-events: none;
}

.space-avatar-letter {
  position: relative;
  z-index: 1;
  color: #fff;
  font-size: 14px;
  font-weight: 600;
  font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif;
}
</style>
