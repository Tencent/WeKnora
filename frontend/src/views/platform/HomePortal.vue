<template>
  <div class="home-portal-container">
    <div class="portal-header">
      <h1 class="portal-title">GRIDORA</h1>
      <h2 class="portal-greeting">今天想完成什么工作？</h2>
      <div class="portal-search-bar">
        <t-input v-model="searchQuery" placeholder="搜索智能体或描述任务，例如：检查这份施工方案的安全风险" size="large">
          <template #prefixIcon>
            <t-icon name="search" />
          </template>
        </t-input>
      </div>
    </div>

    <!-- 行业专业分类 -->
    <div class="portal-categories">
      <t-radio-group v-model="activeCategory" variant="default-filled" size="large">
        <t-radio-button value="all">全部</t-radio-button>
        <t-radio-button value="safety">安全生产</t-radio-button>
        <t-radio-button value="maintenance">设备运检</t-radio-button>
        <t-radio-button value="transmission">输变配电</t-radio-button>
        <t-radio-button value="marketing">营销</t-radio-button>
        <t-radio-button value="infrastructure">基建</t-radio-button>
        <t-radio-button value="rd">研发办公</t-radio-button>
      </t-radio-group>
    </div>

    <!-- 推荐与常用列表 -->
    <div class="portal-section">
      <h3 class="section-title">推荐智能体</h3>
      <div class="agent-card-grid">
        <!-- 假数据卡片渲染 -->
        <div class="agent-card" v-for="agent in mockAgents" :key="agent.id">
          <div class="agent-card-header">
            <div class="agent-avatar"><t-icon name="user-avatar" size="24px"/></div>
            <div class="agent-info">
              <h4>{{ agent.name }}</h4>
              <t-tag theme="success" variant="light" size="small">{{ agent.status }}</t-tag>
            </div>
          </div>
          <div class="agent-card-body">
            <p class="agent-desc">{{ agent.desc }}</p>
            <p class="agent-meta">{{ agent.category }} · 知识更新: {{ agent.updateTime }}</p>
          </div>
          <div class="agent-card-footer">
            <t-button variant="base" theme="primary" block @click="useAgent(agent.id)">立即使用</t-button>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue';

const searchQuery = ref('');
const activeCategory = ref('all');

const mockAgents = ref([
  {
    id: '1',
    name: '规程制度助手',
    desc: '查询电力规程、制度和标准，回答附原文依据。',
    category: '安全生产 · 运检 · 项目管理',
    updateTime: '2026-08',
    status: '已评审'
  },
  {
    id: '2',
    name: '项目资料审阅助手',
    desc: '检查方案、合同和招采材料中的缺项、矛盾与风险。',
    category: '项目管理 · 基建 · 研发',
    updateTime: '2026-08',
    status: '试运行'
  },
  {
    id: '3',
    name: '设备缺陷知识助手',
    desc: '给出可能原因、排查步骤和资料依据，不进行正式缺陷定级。',
    category: '设备运检 · 输变配电',
    updateTime: '2026-08',
    status: '已评审'
  }
]);

const useAgent = (id: string) => {
  // TODO: 跳转至对话页 /platform/creatChat?agentId=id 
  console.log('Using agent', id);
};
</script>

<style scoped>
.home-portal-container {
  padding: 40px;
  max-width: 1200px;
  margin: 0 auto;
}
.portal-header {
  text-align: center;
  margin-bottom: 40px;
}
.portal-title {
  font-size: 24px;
  color: #0B5CAD;
  font-weight: 600;
  margin-bottom: 10px;
  letter-spacing: 1px;
}
.portal-greeting {
  font-size: 32px;
  color: #082F49;
  margin-bottom: 30px;
}
.portal-search-bar {
  max-width: 700px;
  margin: 0 auto;
}
.portal-categories {
  display: flex;
  justify-content: center;
  margin-bottom: 40px;
}
.section-title {
  font-size: 20px;
  color: #082F49;
  margin-bottom: 20px;
}
.agent-card-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(320px, 1fr));
  gap: 24px;
}
.agent-card {
  border: 1px solid #e0e6ed;
  border-radius: 8px;
  padding: 20px;
  background: #fff;
  transition: all 0.3s;
}
.agent-card:hover {
  box-shadow: 0 4px 12px rgba(11, 92, 173, 0.1);
  border-color: #0B5CAD;
}
.agent-card-header {
  display: flex;
  align-items: center;
  margin-bottom: 16px;
}
.agent-avatar {
  width: 40px;
  height: 40px;
  background: #F4F8FC;
  border-radius: 8px;
  display: flex;
  align-items: center;
  justify-content: center;
  margin-right: 12px;
  color: #0B5CAD;
}
.agent-info h4 {
  margin: 0 0 4px 0;
  font-size: 16px;
  color: #082F49;
}
.agent-desc {
  font-size: 14px;
  color: #555;
  line-height: 1.5;
  height: 42px;
  overflow: hidden;
  margin-bottom: 12px;
}
.agent-meta {
  font-size: 12px;
  color: #888;
  margin-bottom: 20px;
}
</style>
