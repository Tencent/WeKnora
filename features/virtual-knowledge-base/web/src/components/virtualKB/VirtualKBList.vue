<template>
  <t-card title="虚拟知识库列表" class="virtual-kb-list">
    <LoadingState v-if="loading" label="加载虚拟知识库中" />
    <ErrorState v-else-if="error" :message="error" :onRetry="loadVirtualKBs" />
    <div v-else>
      <t-empty v-if="!vkbs.length" description="暂无虚拟知识库" />
      <t-list v-else :split="true">
        <t-list-item v-for="item in vkbs" :key="item.id">
          <div class="item">
            <div class="info">
              <div class="title">{{ item.name }}</div>
              <div class="description" v-if="item.description">{{ item.description }}</div>
              <div class="filters" v-if="item.filters.length">
                <div v-for="filter in item.filters" :key="filterKey(item, filter)" class="filter">
                  分类 ID: {{ filter.tag_category_id }} · 操作符: {{ filter.operator }} · 权重: {{ filter.weight }}
                  <div class="tags">标签 ID: {{ filter.tag_ids.join(", ") }}</div>
                </div>
              </div>
            </div>
            <div class="actions">
              <t-space size="small">
                <t-button size="small" variant="outline" @click="emit('edit', item)">编辑</t-button>
                <t-popconfirm content="确认删除该虚拟知识库？" theme="danger" @confirm="emit('delete', item.id)">
                  <t-button size="small" theme="danger" variant="outline">删除</t-button>
                </t-popconfirm>
              </t-space>
            </div>
          </div>
        </t-list-item>
      </t-list>
    </div>
  </t-card>
</template>

<script setup lang="ts">
import { onMounted, ref } from "vue";
import LoadingState from "@components/common/LoadingState.vue";
import ErrorState from "@components/common/ErrorState.vue";
import type { VirtualKB } from "@api/virtualKB";
import { fetchVirtualKBs } from "@api/virtualKB";

const emit = defineEmits<{ (e: "edit", vkb: VirtualKB): void; (e: "delete", id: number): void; (e: "loaded", list: VirtualKB[]): void }>();

const vkbs = ref<VirtualKB[]>([]);
const loading = ref(false);
const error = ref<string | null>(null);

const loadVirtualKBs = async () => {
  try {
    loading.value = true;
    const list = await fetchVirtualKBs();
    vkbs.value = list;
    emit("loaded", list);
    error.value = null;
  } catch (err) {
    error.value = (err as Error).message ?? "加载虚拟知识库失败";
  } finally {
    loading.value = false;
  }
};

const filterKey = (item: VirtualKB, filter: VirtualKB["filters"][number]) => `${item.id}-${filter.tag_category_id}-${filter.operator}-${filter.weight}`;

onMounted(() => {
  loadVirtualKBs();
});

defineExpose({
  loadVirtualKBs,
});
</script>

<style scoped>
.virtual-kb-list {
  min-height: 400px;
}

.item {
  display: flex;
  justify-content: space-between;
  gap: 16px;
}

.info {
  flex: 1;
}

.title {
  font-weight: 600;
}

.description {
  color: var(--td-text-color-secondary);
  margin-top: 4px;
}

.filters {
  margin-top: 8px;
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.filter {
  font-size: 12px;
  color: var(--td-text-color-secondary);
}

.tags {
  margin-top: 2px;
}

.actions {
  display: flex;
  align-items: center;
}
</style>
