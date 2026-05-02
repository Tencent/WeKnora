<template>
  <t-dialog
    v-model:visible="dialogVisible"
    :header="$t('mcp.testResult.title', { name: serviceName })"
    width="600px"
    :footer="false"
  >
    <div v-if="result" class="test-result">
      <!-- Success/Error Status -->
      <div class="status-section">
        <div v-if="result.success" class="status-success">
          <t-icon name="check-circle-filled" size="20px" />
          <span class="status-text">{{ $t('mcp.testResult.connectionSuccess') }}</span>
        </div>
        <div v-else class="status-error">
          <t-icon name="close-circle-filled" size="20px" />
          <span class="status-text">{{ $t('mcp.testResult.connectionFailed') }}</span>
        </div>
        <p v-if="result.message" class="status-message">{{ result.message }}</p>
      </div>

      <!-- Details Section -->
      <div v-if="result.success" class="details-section">
        <!-- Tools List -->
        <div v-if="result.tools && result.tools.length > 0" class="section">
          <div class="section-header">
            <h3>{{ $t('mcp.testResult.toolsTitle') }}</h3>
            <t-tag theme="primary" variant="light" size="small">{{ result.tools.length }}</t-tag>
          </div>
          <div class="tools-grid">
            <div
              v-for="(tool, index) in result.tools"
              :key="index"
              class="tool-card"
              :class="{ 'tool-card-expanded': expandedToolIndex === index }"
            >
              <div class="tool-card-header" @click="toggleTool(index)">
                <div class="tool-header-left">
                  <t-icon name="tools" class="tool-icon" />
                  <div class="tool-info">
                    <div class="tool-name">{{ tool.name }}</div>
                    <div v-if="tool.description" class="tool-desc-preview">
                      {{ tool.description }}
                    </div>
                  </div>
                </div>
                <t-icon
                  :name="expandedToolIndex === index ? 'chevron-up' : 'chevron-down'"
                  class="expand-icon"
                />
              </div>
              <div v-if="expandedToolIndex === index" class="tool-card-content">
                <div v-if="tool.description" class="tool-description">
                  <div class="label">{{ $t('mcp.testResult.descriptionLabel') }}</div>
                  <div class="value">{{ tool.description }}</div>
                </div>
                <div v-if="tool.inputSchema" class="tool-schema">
                  <div class="label">{{ $t('mcp.testResult.schemaLabel') }}</div>
                  <div class="schema-content">
                    <pre>{{ formatSchema(tool.inputSchema) }}</pre>
                  </div>
                </div>
              </div>
            </div>
          </div>
        </div>

        <!-- Resources List -->
        <div v-if="result.resources && result.resources.length > 0" class="section">
          <div class="section-header">
            <h3>{{ $t('mcp.testResult.resourcesTitle') }}</h3>
            <t-tag theme="primary" variant="light" size="small">{{ result.resources.length }}</t-tag>
          </div>
          <div class="resources-grid">
            <div
              v-for="(resource, index) in result.resources"
              :key="index"
              class="resource-card"
            >
              <div class="resource-header">
                <t-icon name="file" class="resource-icon" />
                <div class="resource-info">
                  <div class="resource-name">{{ resource.name || resource.uri }}</div>
                  <div v-if="resource.description" class="resource-desc">
                    {{ resource.description }}
                  </div>
                </div>
              </div>
              <div class="resource-meta">
                <div v-if="resource.uri" class="resource-uri">
                  <t-icon name="link" size="14px" />
                  <span>{{ resource.uri }}</span>
                </div>
                <t-tag v-if="resource.mimeType" theme="default" variant="light-outline" size="small">
                  {{ resource.mimeType }}
                </t-tag>
              </div>
            </div>
          </div>
        </div>

        <!-- Empty State -->
        <div
          v-if="
            (!result.tools || result.tools.length === 0) &&
            (!result.resources || result.resources.length === 0)
          "
          class="empty-state"
        >
          <t-empty :description="$t('mcp.testResult.emptyDescription')" />
        </div>
      </div>
    </div>

    <template #footer>
      <t-button @click="handleClose">{{ $t('common.close') }}</t-button>
    </template>
  </t-dialog>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import type { MCPTestResult } from '@/api/mcp-service'
import { useI18n } from 'vue-i18n'

interface Props {
  visible: boolean
  result: MCPTestResult | null
  serviceName: string
}

interface Emits {
  (e: 'update:visible', value: boolean): void
}

const props = defineProps<Props>()
const emit = defineEmits<Emits>()

const expandedToolIndex = ref<number | null>(null)
const { t } = useI18n()

const dialogVisible = computed({
  get: () => props.visible,
  set: (value) => emit('update:visible', value)
})

const toggleTool = (index: number) => {
  if (expandedToolIndex.value === index) {
    expandedToolIndex.value = null
  } else {
    expandedToolIndex.value = index
  }
}

const formatSchema = (schema: any): string => {
  if (!schema) return ''
  return JSON.stringify(schema, null, 2)
}

const handleClose = () => {
  dialogVisible.value = false
  expandedToolIndex.value = null
}
</script>

<style scoped lang="less">
.test-result {
  padding: 20px 0;

  .status-section {
    margin-bottom: 24px;

    .status-success,
    .status-error {
      display: flex;
      align-items: center;
      gap: 8px;
      margin-bottom: 12px;
      padding: 12px 16px;
      border-radius: 6px;
      background: var(--td-bg-color-secondarycontainer);

      :deep(.t-icon) {
        font-size: 20px;
      }

      .status-text {
        font-size: 15px;
        font-weight: 500;
      }
    }

    .status-success {
      :deep(.t-icon) {
        color: var(--td-success-color);
      }

      .status-text {
        color: var(--td-success-color);
      }
    }

    .status-error {
      :deep(.t-icon) {
        color: var(--td-error-color);
      }

      .status-text {
        color: var(--td-error-color);
      }
    }

    .status-message {
      margin: 0;
      padding: 12px 16px;
      background: var(--td-bg-color-secondarycontainer);
      border-radius: 6px;
      font-size: 13px;
      color: var(--td-text-color-secondary);
      line-height: 1.6;
      word-break: break-word;
    }
  }

  .details-section {
    .section {
      margin-bottom: 30px;

      .section-header {
        display: flex;
        align-items: center;
        gap: 8px;
        margin-bottom: 16px;

        h3 {
          font-size: 16px;
          font-weight: 600;
          margin: 0;
          color: var(--td-text-color-primary);
        }
      }

      .tools-grid {
        display: flex;
        flex-direction: column;
        gap: 12px;
      }

      .tool-card {
        border: 1px solid var(--td-component-stroke);
        border-radius: 8px;
        background: var(--td-bg-color-container);
        transition: all 0.2s ease;
        overflow: hidden;

        &:hover {
          border-color: var(--td-brand-color);
          box-shadow: 0 2px 8px var(--td-brand-color-light);
        }

        &.tool-card-expanded {
          border-color: var(--td-brand-color);
          box-shadow: 0 2px 12px var(--td-brand-color-light);
        }

        .tool-card-header {
          display: flex;
          align-items: center;
          justify-content: space-between;
          padding: 14px 16px;
          cursor: pointer;
          user-select: none;

          .tool-header-left {
            display: flex;
            align-items: flex-start;
            gap: 12px;
            flex: 1;
            min-width: 0;

            .tool-icon {
              color: var(--td-brand-color);
              font-size: 18px;
              margin-top: 2px;
              flex-shrink: 0;
            }

            .tool-info {
              flex: 1;
              min-width: 0;

              .tool-name {
                font-size: 15px;
                font-weight: 600;
                color: var(--td-text-color-primary);
                margin-bottom: 4px;
                word-break: break-word;
              }

              .tool-desc-preview {
                font-size: 13px;
                color: var(--td-text-color-placeholder);
                line-height: 1.5;
                display: -webkit-box;
                -webkit-line-clamp: 2;
                -webkit-box-orient: vertical;
                overflow: hidden;
                text-overflow: ellipsis;
              }
            }
          }

          .expand-icon {
            color: var(--td-text-color-placeholder);
            font-size: 16px;
            flex-shrink: 0;
            transition: transform 0.2s ease;
          }
        }

        .tool-card-content {
          padding: 0 16px 16px 16px;
          border-top: 1px solid var(--td-bg-color-secondarycontainer);
          margin-top: 12px;
          padding-top: 16px;
          animation: slideDown 0.2s ease;

          .tool-description,
          .tool-schema {
            margin-bottom: 16px;

            &:last-child {
              margin-bottom: 0;
            }

            .label {
              font-size: 12px;
              font-weight: 600;
              color: var(--td-text-color-placeholder);
              text-transform: uppercase;
              letter-spacing: 0.5px;
              margin-bottom: 8px;
            }

            .value {
              font-size: 14px;
              color: var(--td-text-color-secondary);
              line-height: 1.6;
            }

            .schema-content {
              background: var(--td-bg-color-secondarycontainer);
              border: 1px solid var(--td-component-stroke);
              border-radius: 6px;
              overflow: hidden;

              pre {
                margin: 0;
                padding: 12px;
                overflow-x: auto;
                font-size: 12px;
                font-family: var(--app-font-family-mono);
                color: var(--td-text-color-primary);
                line-height: 1.6;
                background: transparent;
                border: none;
              }
            }
          }
        }
      }

      .resources-grid {
        display: flex;
        flex-direction: column;
        gap: 12px;
      }

      .resource-card {
        border: 1px solid var(--td-component-stroke);
        border-radius: 8px;
        background: var(--td-bg-color-container);
        padding: 14px 16px;
        transition: all 0.2s ease;

        &:hover {
          border-color: var(--td-brand-color);
          box-shadow: 0 2px 8px var(--td-brand-color-light);
        }

        .resource-header {
          display: flex;
          align-items: flex-start;
          gap: 12px;
          margin-bottom: 12px;

          .resource-icon {
            color: var(--td-brand-color);
            font-size: 18px;
            margin-top: 2px;
            flex-shrink: 0;
          }

          .resource-info {
            flex: 1;
            min-width: 0;

            .resource-name {
              font-size: 15px;
              font-weight: 600;
              color: var(--td-text-color-primary);
              margin-bottom: 4px;
              word-break: break-word;
            }

            .resource-desc {
              font-size: 13px;
              color: var(--td-text-color-placeholder);
              line-height: 1.5;
            }
          }
        }

        .resource-meta {
          display: flex;
          align-items: center;
          justify-content: space-between;
          gap: 12px;
          padding-top: 12px;
          border-top: 1px solid var(--td-bg-color-secondarycontainer);

          .resource-uri {
            display: flex;
            align-items: center;
            gap: 6px;
            flex: 1;
            min-width: 0;
            font-size: 12px;
            color: var(--td-text-color-placeholder);

            :deep(.t-icon) {
              color: var(--td-text-color-placeholder);
            }

            span {
              overflow: hidden;
              text-overflow: ellipsis;
              white-space: nowrap;
            }
          }
        }
      }
    }

    .empty-state {
      padding: 40px 0;
    }
  }
}

@keyframes slideDown {
  from {
    opacity: 0;
    transform: translateY(-10px);
  }
  to {
    opacity: 1;
    transform: translateY(0);
  }
}
</style>

