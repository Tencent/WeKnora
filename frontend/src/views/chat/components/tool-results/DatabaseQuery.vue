<template>
  <div class="database-query-display">
    <!-- Query Display -->
    <div v-if="data.query" class="query-section">
      <div class="section-header">{{ $t('chat.sqlQueryExecuted') }}</div>
      <pre class="query-code">{{ data.query }}</pre>
    </div>
    
    <!-- Results Summary -->
    <div class="results-summary">
      <strong>{{ $t('chat.sqlResultsLabel') }}</strong> {{ data.row_count }} {{ $t('chat.rowsLabel') }}
      <span v-if="data.columns"> × {{ data.columns.length }} {{ $t('chat.columnsLabel') }}</span>
    </div>
    
    <!-- Results Table -->
    <div v-if="data.rows && data.rows.length > 0" class="results-table-container">
      <table class="results-table">
        <thead>
          <tr>
            <th v-for="column in data.columns" :key="column">{{ column }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="(row, index) in data.rows" :key="index">
            <td v-for="column in data.columns" :key="column">
              {{ formatValue(row[column]) }}
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    
    <!-- No Results -->
    <div v-else class="no-results">
      {{ $t('chat.noDatabaseRecords') }}
    </div>
  </div>
</template>

<script setup lang="ts">
import type { DatabaseQueryData } from '@/types/tool-results';
import { useI18n } from 'vue-i18n';

interface Props {
  data: DatabaseQueryData;
}

const props = defineProps<Props>();
const { t } = useI18n();

const formatValue = (value: any): string => {
  if (value === null || value === undefined) {
    return t('chat.nullValuePlaceholder');
  }
  if (typeof value === 'object') {
    return JSON.stringify(value);
  }
  return String(value);
};
</script>

<style lang="less" scoped>
.database-query-display {
  font-size: 13px;
  color: var(--td-text-color-primary);
}

.query-section {
  margin-bottom: 16px;
}

.section-header {
  font-weight: 600;
  color: var(--td-text-color-primary);
  margin-bottom: 8px;
  font-size: 13px;
}

.query-code {
  background: var(--td-bg-color-container);
  color: var(--td-text-color-primary);
  padding: 12px;
  border-radius: 6px;
  overflow-x: auto;
  font-family: 'Monaco', 'Menlo', 'Ubuntu Mono', monospace;
  font-size: 12px;
  line-height: 1.5;
  margin: 0;
  white-space: pre-wrap;
  word-break: break-word;
}

.results-summary {
  padding: 10px 12px;
  background: var(--td-brand-color-light);
  border-left: 3px solid var(--td-brand-color);
  border-radius: 4px;
  margin-bottom: 16px;
  font-size: 13px;
  
  strong {
    color: var(--td-brand-color);
    font-weight: 600;
  }
}

.results-table-container {
  overflow-x: auto;
  border: 1px solid var(--td-component-stroke);
  border-radius: 6px;
  background: var(--td-bg-color-container);
}

.results-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 12px;
  
  thead {
    background: var(--td-bg-color-secondarycontainer);
    border-bottom: 2px solid var(--td-component-stroke);
    
    th {
      padding: 10px 12px;
      text-align: left;
      font-weight: 600;
      color: var(--td-text-color-primary);
      white-space: nowrap;
    }
  }
  
  tbody {
    tr {
      border-bottom: 1px solid var(--td-component-stroke);
      
      &:hover {
        background: var(--td-bg-color-secondarycontainer);
      }
      
      &:last-child {
        border-bottom: none;
      }
    }
    
    td {
      padding: 10px 12px;
      color: var(--td-text-color-primary);
      vertical-align: top;
      max-width: 400px;
      overflow: hidden;
      text-overflow: ellipsis;
    }
  }
}

.no-results {
  padding: 32px;
  text-align: center;
  color: var(--td-text-color-placeholder);
  font-style: italic;
  background: var(--td-bg-color-secondarycontainer);
  border-radius: 6px;
  border: 1px solid var(--td-component-stroke);
}
</style>

