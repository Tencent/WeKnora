<template>
  <div class="virtual-kb-management">
    <t-row :gutter="24">
      <t-col :xs="12" :md="5" :lg="4">
        <VirtualKBList ref="listRef" @edit="handleEdit" @delete="handleDelete" @loaded="handleLoaded" />
      </t-col>
      <t-col :xs="12" :md="7" :lg="8">
        <VirtualKBEditor :initial-value="editing" @submit="handleSubmit" @reset="handleReset" />
      </t-col>
    </t-row>

    <t-notification v-model:visible="notification.visible" :theme="notification.theme" :title="notification.title" :content="notification.content" placement="bottom-right" />
  </div>
</template>

<script setup lang="ts">
import { ref } from "vue";
import VirtualKBList from "@components/virtualKB/VirtualKBList.vue";
import VirtualKBEditor from "@components/virtualKB/VirtualKBEditor.vue";
import type { VirtualKB, VirtualKBCreateRequest } from "@api/virtualKB";
import { createVirtualKB, updateVirtualKB, deleteVirtualKB } from "@api/virtualKB";

const listRef = ref<InstanceType<typeof VirtualKBList> | null>(null);
const virtualKBs = ref<VirtualKB[]>([]);
const editing = ref<VirtualKB | null>(null);

const notification = ref({
  visible: false,
  theme: "success" as "success" | "error",
  title: "",
  content: "",
});

const showNotification = (theme: "success" | "error", title: string, content: string) => {
  notification.value = { visible: true, theme, title, content };
};

const handleLoaded = (list: VirtualKB[]) => {
  virtualKBs.value = list;
};

const handleEdit = (vkb: VirtualKB) => {
  editing.value = vkb;
};

const handleReset = () => {
  editing.value = null;
};

const refreshList = async () => {
  await listRef.value?.loadVirtualKBs();
};

const handleSubmit = async (payload: VirtualKBCreateRequest) => {
  try {
    if (editing.value) {
      await updateVirtualKB(editing.value.id, { ...payload, id: editing.value.id });
      showNotification("success", "更新成功", "虚拟知识库已更新");
    } else {
      await createVirtualKB(payload);
      showNotification("success", "创建成功", "已创建新的虚拟知识库");
    }
    editing.value = null;
    await refreshList();
  } catch (err) {
    showNotification("error", "操作失败", (err as Error).message ?? "保存失败");
  }
};

const handleDelete = async (id: number) => {
  try {
    await deleteVirtualKB(id);
    showNotification("success", "删除成功", "虚拟知识库已删除");
    if (editing.value?.id === id) {
      editing.value = null;
    }
    await refreshList();
  } catch (err) {
    showNotification("error", "删除失败", (err as Error).message ?? "删除虚拟知识库失败");
  }
};
</script>

<style scoped>
.virtual-kb-management {
  padding: 24px;
}
</style>
