<template>
  <div v-if="visible" class="mention-menu" :style="style" ref="menuRef" @click.stop @scroll="onScroll">
    <!-- Knowledge Bases Group -->
    <div v-if="kbItems.length > 0" class="mention-group">
      <div class="mention-group-header">{{ $t('common.knowledgeBase') }}</div>
      <div 
        v-for="(item, index) in kbItems" 
        :key="item.id"
        class="mention-item"
        :class="{ active: index === activeIndex }"
        @click="$emit('select', item)"
        @mouseenter="$emit('update:activeIndex', index)"
      >
        <div class="icon" :class="item.kbType === 'faq' ? 'faq-icon' : 'kb-icon'">
          <t-icon :name="item.kbType === 'faq' ? 'chat-bubble-help' : 'folder'" />
        </div>
        <span class="name">{{ item.name }}</span>
        <span class="count">({{ item.count || 0 }})</span>
      </div>
    </div>
    
    <!-- Files Group -->
    <div v-if="fileItems.length > 0" class="mention-group">
      <div class="mention-group-header">{{ $t('common.file') }}</div>
      <div 
        v-for="(item, index) in fileItems" 
        :key="item.id"
        class="mention-item"
        :class="{ active: (kbItems.length + index) === activeIndex }"
        @click="$emit('select', item)"
        @mouseenter="$emit('update:activeIndex', kbItems.length + index)"
      >
        <div class="icon file-icon">
          <t-icon name="file" />
        </div>
        <span class="name">{{ item.name }}</span>
        <span v-if="item.kbName" class="kb-name">{{ item.kbName }}</span>
      </div>
      <!-- Loading indicator -->
      <div v-if="loading" class="loading-more">
        <t-loading size="small" />
      </div>
    </div>
    
    <div v-if="items.length === 0 && !loading" class="empty">
      {{ $t('common.noResult') }}
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, watch, ref, nextTick } from 'vue';

const props = defineProps<{
  visible: boolean;
  style: any;
  items: Array<{ id: string; name: string; type: 'kb' | 'file'; kbType?: 'document' | 'faq'; count?: number; kbName?: string }>;
  activeIndex: number;
  hasMore?: boolean;
  loading?: boolean;
}>();

const emit = defineEmits(['select', 'update:activeIndex', 'loadMore']);

const menuRef = ref<HTMLElement | null>(null);

const kbItems = computed(() => props.items.filter(item => item.type === 'kb'));
const fileItems = computed(() => props.items.filter(item => item.type === 'file'));

const onScroll = (e: Event) => {
  const target = e.target as HTMLElement;
  const { scrollTop, scrollHeight, clientHeight } = target;
  // Load more when scrolled to bottom (with 50px threshold)
  if (scrollHeight - scrollTop - clientHeight < 50 && props.hasMore && !props.loading) {
    emit('loadMore');
  }
};

watch(() => props.activeIndex, (newIndex) => {
  scrollToItem(newIndex);
});

watch(() => props.visible, (newVisible) => {
  if (newVisible) {
    nextTick(() => {
      if (menuRef.value) menuRef.value.scrollTop = 0;
      scrollToItem(props.activeIndex);
    });
  }
});

const scrollToItem = (index: number) => {
  nextTick(() => {
    if (!menuRef.value) return;
    
    const items = menuRef.value.querySelectorAll('.mention-item');
    if (!items || items.length <= index) return;
    
    const activeItem = items[index] as HTMLElement;
    const menu = menuRef.value;
    
    if (activeItem) {
      const menuRect = menu.getBoundingClientRect();
      const itemRect = activeItem.getBoundingClientRect();
      
      // 检查是否在上方被遮挡
      if (itemRect.top < menuRect.top) {
        menu.scrollTop -= (menuRect.top - itemRect.top);
      }
      // 检查是否在下方被遮挡
      else if (itemRect.bottom > menuRect.bottom) {
        menu.scrollTop += (itemRect.bottom - menuRect.bottom);
      }
    }
  });
};
</script>

<style scoped>
.mention-menu {
  position: fixed;
  z-index: 10000;
  background: var(--td-bg-color-container, #fff);
  border: 1px solid var(--td-component-border, #e7e9eb);
  border-radius: var(--td-radius-medium, 6px);
  box-shadow: var(--td-shadow-2, 0 3px 14px 2px rgba(0, 0, 0, 0.05));
  width: 300px;
  max-height: 360px;
  overflow-y: auto;
  display: flex;
  flex-direction: column;
  padding: 4px 0;
}

.mention-group {
  padding: 4px 0;
}

.mention-group:not(:last-child) {
  border-bottom: 1px solid var(--td-component-border, #f0f0f0);
}

.mention-group-header {
  padding: 8px 12px 4px;
  font-size: var(--td-font-size-mark-small, 12px);
  font-weight: 600;
  color: var(--td-text-color-secondary, #999);
}

.mention-item {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 12px;
  margin: 0 4px;
  cursor: pointer;
  border-radius: var(--td-radius-default, 3px);
  color: var(--td-text-color-primary, #333);
  font-size: var(--td-font-size-body-medium, 14px);
  font-family: var(--td-font-family, "PingFang SC");
  transition: background 0.2s cubic-bezier(0.38, 0, 0.24, 1);
}

.mention-item:hover {
  background: var(--td-bg-color-container-hover, #f3f3f3);
}

.mention-item.active {
  background: var(--td-brand-color-light, #e9f8ec);
  color: var(--td-brand-color, #07c05f);
}

.icon {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 20px;
  height: 20px;
  border-radius: var(--td-radius-small, 2px);
  flex-shrink: 0;
  /* background: var(--td-bg-color-secondarycontainer, #f3f3f3); */
}

/* Document KB - Greenish */
.kb-icon {
  background: rgba(16, 185, 129, 0.1);
  color: #10b981;
}

/* FAQ KB - Blueish */
.faq-icon {
  background: rgba(0, 82, 217, 0.1);
  color: #0052d9;
}

/* File - Orange */
.file-icon {
  background: rgba(237, 123, 47, 0.1);
  color: #ed7b2f;
}

.mention-item.active .icon {
  /* Active state keeps the colored icon but maybe adjusts background or just inherits */
  background: transparent;
  color: inherit;
}

.name {
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.count {
  flex-shrink: 0;
  font-size: var(--td-font-size-mark-small, 12px);
  color: var(--td-text-color-secondary, #999);
}

.kb-name {
  flex-shrink: 0;
  max-width: 80px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: var(--td-font-size-mark-small, 12px);
  color: var(--td-text-color-secondary, #999);
}

.empty {
  padding: 24px 12px;
  text-align: center;
  color: var(--td-text-color-placeholder, #999);
  font-size: var(--td-font-size-body-medium, 14px);
}

.loading-more {
  display: flex;
  justify-content: center;
  padding: 8px 12px;
}
</style>
