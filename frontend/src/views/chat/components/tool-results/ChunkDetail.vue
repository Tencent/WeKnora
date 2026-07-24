<template>
  <div class="chunk-detail">
    <div class="chunk-detail-sidebar">
      <div
        v-for="tab in tabs"
        :key="tab.key"
        class="sidebar-item"
        :class="{ active: activeTab === tab.key }"
        @click="activeTab = tab.key"
      >
        {{ tab.label }}
      </div>
    </div>
    <div class="chunk-detail-body">
      <!-- 内容 tab -->
      <div v-if="activeTab === 'content'" class="tab-panel">
        <div class="info-section-title">基础信息</div>
        <div class="basic-info-grid">
          <div class="basic-info-field">
            <span class="basic-label">{{ $t("chat.chunkIdLabel") }}</span>
            <span class="basic-value"
              ><code>{{ data.chunk_id }}</code></span
            >
          </div>
          <div class="basic-info-field">
            <span class="basic-label">{{ $t("chat.documentIdLabel") }}</span>
            <span class="basic-value"
              ><code>{{ data.knowledge_id }}</code></span
            >
          </div>
          <div class="basic-info-field">
            <span class="basic-label">{{ $t("chat.positionLabel") }}</span>
            <span class="basic-value">{{
              $t("chat.chunkPositionValue", { index: data.chunk_index })
            }}</span>
          </div>
          <div v-if="data.content_length" class="basic-info-field">
            <span class="basic-label">{{
              $t("chat.contentLengthLabelSimple")
            }}</span>
            <span class="basic-value">{{
              $t("chat.lengthChars", { value: data.content_length })
            }}</span>
          </div>
        </div>

        <div class="info-section-divider"></div>
        <div class="info-section-title">{{ $t("chat.fullContentLabel") }}</div>
        <div class="full-content">{{ data.content }}</div>
        <div class="action-buttons">
          <button class="action-button" @click="copyToClipboard">
            📋 {{ $t("chat.copyContent") }}
          </button>
        </div>
      </div>

      <!-- 反馈统计 tab -->
      <div v-if="activeTab === 'feedback'" class="tab-panel">
        <div class="info-section-title">反馈统计</div>
        <div class="feedback-stats-row">
          <div class="feedback-stat-item">
            <span class="stat-icon">👍</span>
            <span class="stat-value">{{ data.feedback_like_count ?? 0 }}</span>
          </div>
          <div class="feedback-stat-item">
            <span class="stat-icon">👎</span>
            <span class="stat-value">{{
              data.feedback_dislike_count ?? 0
            }}</span>
          </div>
          <div class="feedback-stat-item">
            <span class="stat-icon">📊</span>
            <span class="stat-value">{{ formattedPositiveRate }}</span>
          </div>
        </div>

        <!-- 点踩原因列表 -->
        <div class="info-section-title" style="margin-top: 12px">点踩原因</div>
        <div v-if="dislikeReasonList.length" class="dislike-reasons-list">
          <div
            v-for="(item, idx) in dislikeReasonList"
            :key="idx"
            class="dislike-reason-item"
          >
            <span class="reason-label">{{ item.reason || "未填写" }}</span>
            <span class="reason-count">{{ item.count }} 次</span>
          </div>
        </div>
        <div v-else class="dislike-reasons-empty">暂无点踩原因</div>
      </div>

      <!-- 权重日志 tab -->
      <div v-if="activeTab === 'weight'" class="tab-panel">
        <div class="info-section-title">权重信息</div>
        <div class="weight-info-grid">
          <div class="weight-info-item">
            <span class="weight-label">Recall Weight</span>
            <span class="weight-value">{{ data.recall_weight ?? "-" }}</span>
          </div>
          <div class="weight-info-item">
            <span class="weight-label">质量状态</span>
            <span class="weight-value">{{ data.quality_status || "" }}</span>
          </div>
        </div>

        <div class="info-section-title" style="margin-top: 16px">
          权重调整规则
        </div>
        <div class="weight-rules">
          <table class="weight-table">
            <thead>
              <tr>
                <th>条件</th>
                <th>权重</th>
                <th>状态</th>
              </tr>
            </thead>
            <tbody>
              <tr>
                <td>好评率 ≥ 80%</td>
                <td>× 1.2</td>
                <td>
                  <span class="status-badge status-boosted">boosted</span>
                </td>
              </tr>
              <tr>
                <td>80% &gt; 好评率 ≥ 50%</td>
                <td>× 1.0</td>
                <td><span class="status-badge status-normal">normal</span></td>
              </tr>
              <tr>
                <td>50% &gt; 好评率 ≥ 20%</td>
                <td>× 0.7</td>
                <td>
                  <span class="status-badge status-deprioritized"
                    >deprioritized</span
                  >
                </td>
              </tr>
              <tr>
                <td>好评率 &lt; 20%</td>
                <td>× 0.7</td>
                <td>
                  <span class="status-badge status-needs-opt"
                    >needs_optimization</span
                  >
                </td>
              </tr>
            </tbody>
          </table>
        </div>

        <div class="info-section-title" style="margin-top: 16px">
          权重变更记录
        </div>
        <div class="weight-history">
          <!-- Loading -->
          <div v-if="weightLogsLoading" class="weight-history-empty">
            加载中...
          </div>

          <!-- Error -->
          <div v-else-if="weightLogsError" class="weight-history-empty">
            {{ weightLogsError }}
          </div>

          <!-- Empty -->
          <div v-else-if="weightLogs.length === 0" class="weight-history-empty">
            暂无权重变更记录
          </div>

          <!-- History list -->
          <div v-else class="weight-log-list">
            <div
              v-for="(log, idx) in weightLogs"
              :key="log.id || idx"
              class="weight-log-entry"
            >
              <div class="log-header">
                <span class="log-time">{{ formatTime(log.created_at) }}</span>
                <span class="log-trigger">{{ log.triggered_by }}</span>
              </div>
              <div class="log-changes">
                <div class="log-change">
                  <span class="change-label">Recall Weight</span>
                  <span class="change-diff">
                    {{ log.old_recall_weight.toFixed(2) }} →
                    {{ log.new_recall_weight.toFixed(2) }}
                  </span>
                </div>
                <div class="log-change">
                  <span class="change-label">质量状态</span>
                  <span class="change-diff">
                    {{ log.old_quality_status }} →
                    {{ log.new_quality_status }}
                  </span>
                </div>
                <div class="log-change">
                  <span class="change-label">好评率</span>
                  <span class="change-diff">
                    {{ (log.old_positive_rate * 100).toFixed(1) }}% →
                    {{ (log.new_positive_rate * 100).toFixed(1) }}%
                  </span>
                </div>
                <div class="log-change">
                  <span class="change-label">👍/👎</span>
                  <span class="change-diff">
                    {{ log.old_like_count }}/{{ log.old_dislike_count }} →
                    {{ log.new_like_count }}/{{ log.new_dislike_count }}
                  </span>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch, onMounted } from "vue";
import type {
  ChunkDetailData,
  ChunkWeightLogEntry,
} from "@/types/tool-results";
import { getChunkWeightLogs } from "@/api/knowledge-base/index";
import { useI18n } from "vue-i18n";

const props = defineProps<{
  data: ChunkDetailData;
}>();

const { t } = useI18n();

const tabs = [
  { key: "content", label: "内容" },
  { key: "feedback", label: "反馈统计" },
  { key: "weight", label: "权重日志" },
];

const activeTab = ref("content");

// Weight log state
const weightLogs = ref<ChunkWeightLogEntry[]>([]);
const weightLogsLoading = ref(false);
const weightLogsError = ref("");

const fetchWeightLogs = async () => {
  if (!props.data.chunk_id) return;
  weightLogsLoading.value = true;
  weightLogsError.value = "";
  try {
    const result: any = await getChunkWeightLogs(props.data.chunk_id);
    if (result.success && result.data) {
      weightLogs.value = result.data;
    }
  } catch (err: any) {
    weightLogsError.value = err?.message || "加载失败";
  } finally {
    weightLogsLoading.value = false;
  }
};

onMounted(() => {
  fetchWeightLogs();
});

const formattedPositiveRate = computed(() => {
  const rate = props.data.feedback_positive_rate;
  if (rate === undefined || rate === null) return "-";
  return `${(rate * 100).toFixed(1)}%`;
});

const dislikeReasonList = computed(() => {
  return props.data.dislike_reasons || [];
});

const formatTime = (iso: string) => {
  try {
    const d = new Date(iso);
    return d.toLocaleString();
  } catch {
    return iso;
  }
};

const copyToClipboard = () => {
  const text = props.data.content;
  if (navigator.clipboard && navigator.clipboard.writeText) {
    navigator.clipboard.writeText(text).catch(() => {
      fallbackCopy(text);
    });
  } else {
    fallbackCopy(text);
  }
};

function fallbackCopy(text: string) {
  const textArea = document.createElement("textarea");
  textArea.value = text;
  textArea.style.position = "fixed";
  textArea.style.opacity = "0";
  document.body.appendChild(textArea);
  textArea.select();
  document.execCommand("copy");
  document.body.removeChild(textArea);
}
</script>

<style lang="less" scoped>
@import "./tool-results.less";

.chunk-detail {
  display: flex;
  gap: 0;
  height: 420px;
}

.chunk-detail-sidebar {
  width: 90px;
  flex-shrink: 0;
  border-right: 1px solid var(--td-component-stroke);
  display: flex;
  flex-direction: column;
  gap: 0;
  padding: 4px 0;
}

.sidebar-item {
  padding: 10px 12px;
  font-size: 13px;
  cursor: pointer;
  color: var(--td-text-color-secondary);
  transition: all 0.15s ease;
  border-left: 3px solid transparent;
  user-select: none;

  &:hover {
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-text-color-primary);
  }

  &.active {
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-brand-color);
    border-left-color: var(--td-brand-color);
    font-weight: 500;
  }
}

.chunk-detail-body {
  flex: 1;
  padding: 0 0 0 16px;
  overflow-y: auto;
  min-width: 0;
}

code {
  font-family: var(--app-font-family-mono);
  font-size: 11px;
  background: var(--td-bg-color-secondarycontainer);
  padding: 2px 4px;
  border-radius: 3px;
}

.action-buttons {
  display: flex;
  gap: 8px;
  margin-top: 12px;
}

// --- 基础信息 ---
.basic-info-grid {
  display: flex;
  flex-direction: column;
  gap: 6px;
  margin-top: 6px;
}

.basic-info-field {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 12px;
  line-height: 1.5;
}

.basic-label {
  color: var(--td-text-color-secondary);
  min-width: 80px;
  flex-shrink: 0;
  font-weight: 500;
}

.basic-value {
  color: var(--td-text-color-primary);
  word-break: break-word;
}

// --- 反馈统计 ---
.info-section-divider {
  height: 1px;
  background: var(--td-component-stroke);
  margin: 12px 0;
}

.feedback-stats-row {
  display: flex;
  gap: 10px;
  margin-top: 6px;
}

.feedback-stat-item {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 6px 12px;
  background: var(--td-bg-color-secondarycontainer);
  border-radius: 4px;
  font-size: 13px;
}

.stat-icon {
  font-size: 14px;
}

.stat-value {
  font-weight: 600;
  color: var(--td-text-color-primary);
}

// --- 点踩原因列表 ---
.dislike-reasons-list {
  display: flex;
  flex-direction: column;
  gap: 4px;
  margin-top: 6px;
}

.dislike-reason-item {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 6px 10px;
  background: var(--td-bg-color-secondarycontainer);
  border-radius: 4px;
  font-size: 12px;
}

.reason-label {
  color: var(--td-text-color-primary);
}

.reason-count {
  color: var(--td-text-color-secondary);
  font-weight: 500;
  flex-shrink: 0;
  margin-left: 12px;
}

.dislike-reasons-empty {
  text-align: center;
  padding: 12px 0;
  color: var(--td-text-color-placeholder);
  font-size: 12px;
}

// --- 权重日志样式 ---
.weight-info-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 8px;
  margin-top: 8px;
}

.weight-info-item {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 8px 10px;
  background: var(--td-bg-color-secondarycontainer);
  border-radius: 4px;
}

.weight-label {
  font-size: 11px;
  color: var(--td-text-color-placeholder);
}

.weight-value {
  font-size: 14px;
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.weight-rules {
  margin-top: 8px;
}

.weight-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 12px;

  th,
  td {
    padding: 6px 10px;
    text-align: left;
    border-bottom: 1px solid var(--td-component-stroke);
  }

  th {
    font-weight: 600;
    color: var(--td-text-color-secondary);
    font-size: 11px;
  }

  td {
    color: var(--td-text-color-primary);
  }
}

.status-badge {
  display: inline-block;
  padding: 1px 6px;
  border-radius: 3px;
  font-size: 11px;
  font-weight: 500;

  &.status-boosted {
    background: rgba(0, 168, 112, 0.12);
    color: var(--td-success-color);
  }

  &.status-normal {
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-text-color-secondary);
  }

  &.status-deprioritized {
    background: rgba(255, 152, 0, 0.12);
    color: var(--td-warning-color);
  }

  &.status-needs-opt {
    background: rgba(244, 67, 54, 0.12);
    color: var(--td-error-color);
  }
}

// --- 权重变更记录 ---
.weight-history {
  margin-top: 8px;
}

.weight-history-empty {
  text-align: center;
  padding: 20px 0;
  color: var(--td-text-color-placeholder);
  font-size: 12px;
}

.weight-log-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
  max-height: 220px;
  overflow-y: auto;
}

.weight-log-entry {
  padding: 8px 10px;
  background: var(--td-bg-color-secondarycontainer);
  border-radius: 4px;
  border-left: 3px solid var(--td-component-stroke);
}

.log-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 6px;
}

.log-time {
  font-size: 11px;
  color: var(--td-text-color-placeholder);
}

.log-trigger {
  font-size: 10px;
  padding: 1px 5px;
  border-radius: 3px;
  background: var(--td-bg-color-container);
  color: var(--td-text-color-secondary);
}

.log-changes {
  display: flex;
  flex-direction: row;
  gap: 8px;
}

.log-change {
  display: flex;
  flex-direction: column;
  align-items: center;
  flex: 1;
  font-size: 11px;
  line-height: 1.5;
  text-align: center;
}

.change-label {
  color: var(--td-text-color-secondary);
}

.change-diff {
  color: var(--td-text-color-primary);
  font-weight: 500;
}
</style>
