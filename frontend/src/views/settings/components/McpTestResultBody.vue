<template>
  <div v-if="result" class="mtr">
    <!-- 状态条 -->
    <div class="mtr-status" :class="result.success ? 'is-success' : 'is-error'">
      <t-icon :name="result.success ? 'check-circle-filled' : 'close-circle-filled'" />
      <span>{{ result.success ? $t('mcp.testResult.connectionSuccess') : $t('mcp.testResult.connectionFailed') }}</span>
    </div>
    <p v-if="result.message" class="mtr-message">{{ result.message }}</p>

    <template v-if="result.success">
      <!-- 工具列表 -->
      <div v-if="result.tools && result.tools.length > 0" class="mtr-group">
        <div class="mtr-group-title">
          <span>{{ $t('mcp.testResult.toolsTitle') }}</span>
          <t-tag theme="primary" variant="light" size="small">{{ result.tools.length }}</t-tag>
        </div>
        <div class="mtr-list">
          <div
            v-for="(tool, index) in displayTools"
            :key="index"
            class="mtr-item"
            :class="{ 'is-open': expandedToolIndex === index }"
          >
            <div class="mtr-item-head" @click="toggleTool(index)">
              <t-icon name="tools" class="mtr-item-icon" />
              <span class="mtr-item-name">{{ tool.name }}</span>
              <div class="mtr-item-actions" @click.stop>
                <t-tooltip v-if="serviceId" :content="$t('mcp.testResult.requireApprovalTip')" placement="top">
                  <span class="mtr-approval">
                    <t-icon name="error-circle-filled" class="mtr-approval-icon" />
                    <span class="mtr-approval-label">{{ $t('mcp.testResult.requireApproval') }}</span>
                    <t-switch
                      :value="tool.require_approval"
                      :loading="approvalLoading[tool.name]"
                      size="small"
                      @change="(v: boolean) => onRequireApprovalChange(tool.name, v)"
                    />
                  </span>
                </t-tooltip>
                <t-icon
                  :name="expandedToolIndex === index ? 'chevron-up' : 'chevron-down'"
                  class="mtr-chevron"
                />
              </div>
            </div>
            <div
              v-if="tool.description"
              class="mtr-item-desc"
              :class="{ 'is-clamped': expandedToolIndex !== index }"
            >
              {{ tool.description }}
            </div>
            <div v-if="expandedToolIndex === index && tool.inputSchema" class="mtr-item-schema">
              <div class="mtr-schema-label">{{ $t('mcp.testResult.schemaLabel') }}</div>
              <pre>{{ formatSchema(tool.inputSchema) }}</pre>
            </div>
          </div>
        </div>
      </div>

      <!-- 资源列表 -->
      <div v-if="result.resources && result.resources.length > 0" class="mtr-group">
        <div class="mtr-group-title">
          <span>{{ $t('mcp.testResult.resourcesTitle') }}</span>
          <t-tag theme="primary" variant="light" size="small">{{ result.resources.length }}</t-tag>
        </div>
        <div class="mtr-list">
          <div v-for="(resource, index) in result.resources" :key="index" class="mtr-item">
            <div class="mtr-item-head is-static">
              <t-icon name="file" class="mtr-item-icon" />
              <span class="mtr-item-name">{{ resource.name || resource.uri }}</span>
              <t-tag v-if="resource.mimeType" theme="default" variant="light-outline" size="small">
                {{ resource.mimeType }}
              </t-tag>
            </div>
            <div v-if="resource.description" class="mtr-item-desc">{{ resource.description }}</div>
            <div v-if="resource.uri" class="mtr-item-uri">
              <t-icon name="link" />
              <span>{{ resource.uri }}</span>
            </div>
          </div>
        </div>
      </div>

      <!-- 空状态 -->
      <t-empty
        v-if="(!result.tools || result.tools.length === 0) && (!result.resources || result.resources.length === 0)"
        :description="$t('mcp.testResult.emptyDescription')"
        class="mtr-empty"
      />
    </template>
  </div>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import type { MCPTestResult, MCPTool } from '@/api/mcp-service'
import { getMCPToolApprovals, setMCPToolApproval } from '@/api/mcp-service'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'

interface Props {
  result: MCPTestResult | null
  /** When set, loads/saves per-tool approval flags */
  serviceId?: string
  /** When true, (re)loads approval flags. Lets the dialog gate the fetch on
   *  visibility; defaults to true for always-rendered inline usage. */
  active?: boolean
}

const props = withDefaults(defineProps<Props>(), { active: true })

const expandedToolIndex = ref<number | null>(null)
const { t } = useI18n()
const displayTools = ref<MCPTool[]>([])
const approvalLoading = ref<Record<string, boolean>>({})

const mergeApprovals = async () => {
  const tools = props.result?.tools
  if (!tools?.length) {
    displayTools.value = []
    return
  }
  if (!props.serviceId) {
    displayTools.value = tools.map((x) => ({ ...x }))
    return
  }
  try {
    const rows = await getMCPToolApprovals(props.serviceId)
    const map = new Map(rows.map((r) => [r.tool_name, r.require_approval]))
    displayTools.value = tools.map((tool) => ({
      ...tool,
      require_approval: map.get(tool.name) || false,
    }))
  } catch {
    displayTools.value = tools.map((x) => ({ ...x }))
  }
}

watch(
  () => [props.active, props.serviceId, props.result?.tools],
  () => {
    if (props.active) {
      void mergeApprovals()
    }
  },
  { deep: true, immediate: true }
)

const onRequireApprovalChange = async (toolName: string, value: boolean) => {
  if (!props.serviceId) return
  approvalLoading.value = { ...approvalLoading.value, [toolName]: true }
  try {
    await setMCPToolApproval(props.serviceId, toolName, value)
    displayTools.value = displayTools.value.map((x) =>
      x.name === toolName ? { ...x, require_approval: value } : x
    )
  } catch (e) {
    console.error(e)
    MessagePlugin.error(t('mcp.testResult.approvalSaveFailed'))
  } finally {
    approvalLoading.value = { ...approvalLoading.value, [toolName]: false }
  }
}

const toggleTool = (index: number) => {
  expandedToolIndex.value = expandedToolIndex.value === index ? null : index
}

const formatSchema = (schema: any): string => {
  if (!schema) return ''
  return JSON.stringify(schema, null, 2)
}
</script>

<style scoped lang="less">
.mtr {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

/* 状态条 */
.mtr-status {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 10px 12px;
  border-radius: 6px;
  font-size: 14px;
  font-weight: 500;

  &.is-success {
    color: var(--td-success-color);
    background: var(--td-success-color-1, var(--td-bg-color-secondarycontainer));
  }

  &.is-error {
    color: var(--td-error-color);
    background: var(--td-error-color-1, var(--td-bg-color-secondarycontainer));
  }

  :deep(.t-icon) {
    font-size: 18px;
  }
}

.mtr-message {
  margin: 0;
  font-size: 13px;
  color: var(--td-text-color-secondary);
  line-height: 1.6;
  word-break: break-word;
}

/* 分组 */
.mtr-group {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.mtr-group-title {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 13px;
  font-weight: 600;
  color: var(--td-text-color-secondary);
}

/* 列表：扁平卡片，靠分隔与圆角，无重阴影，贴合抽屉 */
.mtr-list {
  display: flex;
  flex-direction: column;
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
  overflow: hidden;
}

.mtr-item {
  padding: 12px 14px;
  background: var(--td-bg-color-container);

  & + .mtr-item {
    border-top: 1px solid var(--td-component-stroke);
  }

  &.is-open {
    background: var(--td-bg-color-secondarycontainer);
  }
}

.mtr-item-head {
  display: flex;
  align-items: center;
  gap: 8px;
  cursor: pointer;
  user-select: none;

  &.is-static {
    cursor: default;
  }
}

.mtr-item-icon {
  flex-shrink: 0;
  font-size: 16px;
  color: var(--td-brand-color);
}

.mtr-item-name {
  flex: 1;
  min-width: 0;
  font-size: 14px;
  font-weight: 600;
  color: var(--td-text-color-primary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.mtr-item-actions {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-shrink: 0;
}

.mtr-approval {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  color: var(--td-text-color-secondary);

  .mtr-approval-icon {
    font-size: 15px;
    color: var(--td-warning-color);
  }

  .mtr-approval-label {
    white-space: nowrap;
  }
}

.mtr-chevron {
  font-size: 16px;
  color: var(--td-text-color-placeholder);
  flex-shrink: 0;
}

/* 描述：独占整行，绝不与右侧开关挤在一起 */
.mtr-item-desc {
  margin-top: 6px;
  font-size: 13px;
  color: var(--td-text-color-placeholder);
  line-height: 1.6;
  word-break: break-word;

  &.is-clamped {
    display: -webkit-box;
    -webkit-line-clamp: 2;
    -webkit-box-orient: vertical;
    overflow: hidden;
  }
}

.mtr-item-schema {
  margin-top: 10px;

  .mtr-schema-label {
    font-size: 12px;
    font-weight: 600;
    color: var(--td-text-color-placeholder);
    text-transform: uppercase;
    letter-spacing: 0.5px;
    margin-bottom: 6px;
  }

  pre {
    margin: 0;
    padding: 10px 12px;
    border-radius: 6px;
    background: var(--td-bg-color-page);
    border: 1px solid var(--td-component-stroke);
    overflow-x: auto;
    font-size: 12px;
    font-family: var(--app-font-family-mono);
    color: var(--td-text-color-primary);
    line-height: 1.6;
  }
}

.mtr-item-uri {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-top: 8px;
  font-size: 12px;
  color: var(--td-text-color-placeholder);

  span {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
}

.mtr-empty {
  padding: 24px 0;
}
</style>
