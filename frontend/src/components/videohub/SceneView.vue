<template>
  <section class="scene-view" aria-label="场景视图">
    <div class="scene-view__toolbar">
      <div class="scene-view__switch" role="tablist" aria-label="场景类型">
        <button
          v-for="item in sceneOptions"
          :key="item.key"
          type="button"
          role="tab"
          :aria-selected="scene === item.key"
          :class="{ 'is-active': scene === item.key }"
          @click="scene = item.key"
        >
          {{ item.label }}
        </button>
      </div>
      <span class="scene-view__hint">从培训主题与会议议题理解跨视频关系</span>
    </div>

    <div class="scene-view__overview" aria-label="场景概览">
      <div v-for="stat in overviewStats" :key="stat.label" class="scene-stat">
        <span>{{ stat.label }}</span>
        <strong>{{ stat.value }}</strong>
      </div>
    </div>

    <div v-if="scene === 'training'" class="training-layout">
      <section class="scene-panel topic-network-panel">
        <header class="scene-panel__head">
          <strong>培训主题簇</strong>
          <span>每个节点代表一组相关内容</span>
        </header>
        <div class="topic-network">
          <svg viewBox="0 0 900 540" preserveAspectRatio="none" aria-label="主题簇关系">
            <g
              v-for="edge in trainingEdges"
              :key="edge.key"
              class="topic-edge"
              :class="{ 'is-strong': edge.strong, 'is-selected': selectedRelation === edge.key }"
              role="button"
              tabindex="0"
              :aria-label="`${relationTypeLabels[edge.relationType]}：${edge.summary}`"
              @click="selectRelation(edge.key)"
              @keydown.enter="selectRelation(edge.key)"
              @keydown.space.prevent="selectRelation(edge.key)"
            >
              <line class="topic-edge__hit" :x1="edge.x1" :y1="edge.y1" :x2="edge.x2" :y2="edge.y2" />
              <line class="topic-edge__line" :x1="edge.x1" :y1="edge.y1" :x2="edge.x2" :y2="edge.y2" />
            </g>
          </svg>
          <button
            v-for="cluster in trainingClusters"
            :key="cluster.key"
            type="button"
            class="cluster-node"
            :class="{ 'is-selected': selectedCluster === cluster.key }"
            :style="{ left: `${cluster.x}%`, top: `${cluster.y}%` }"
            @click="selectCluster(cluster.key)"
          >
            <strong>{{ cluster.title }}</strong>
            <span>{{ cluster.topics }} 个主题 · {{ cluster.videos }} 个视频</span>
          </button>
          <div class="topic-network__caption">每个节点代表一个培训主题簇；连线表示主题簇之间的学习承接，点击查看簇内内容与学习路径。</div>
        </div>
      </section>

      <aside class="scene-panel training-detail" aria-live="polite">
        <template v-if="activeRelation">
          <div class="scene-kicker">主题簇关系</div>
          <h2>{{ relationTypeLabels[activeRelation.relationType] }}</h2>
          <p class="training-detail__description relation-detail__summary">{{ activeRelation.summary }}</p>
        </template>
        <template v-else>
          <div class="scene-kicker">{{ activeCluster.category }} · 学习路径</div>
          <h2>{{ activeCluster.title }}</h2>
          <p class="training-detail__description">{{ activeCluster.description }}</p>
          <div class="scene-section">
            <span class="scene-section__label">该主题簇的学习路径</span>
            <div class="training-path">
              <button
                v-for="(step, index) in activeCluster.path"
                :key="step.title"
                type="button"
                class="path-step"
                :class="{ 'is-active': selectedPath === index }"
                :aria-expanded="selectedPath === index"
                @click="selectedPath = selectedPath === index ? -1 : index"
              >
                <small>{{ String(index + 1).padStart(2, '0') }} · {{ step.stage }}</small>
                <strong>{{ step.title }}</strong>
                <span>{{ step.explanation }}</span>
                <em>{{ step.meta }}</em>
              </button>
            </div>
            <div v-if="selectedPath >= 0" class="path-detail">
              <span>路径说明</span>
              <strong>{{ activeCluster.path[selectedPath]?.title }}</strong>
              <p>{{ activeCluster.path[selectedPath]?.detail }}</p>
              <button type="button" class="scene-link">查看相关内容</button>
            </div>
          </div>
        </template>
      </aside>
    </div>

    <div v-else class="meeting-layout">
      <section class="scene-panel topic-list" aria-label="会议主题">
        <button
          v-for="topic in meetingTopics"
          :key="topic.key"
          type="button"
          class="topic-item"
          :class="{ 'is-active': selectedTopic === topic.key }"
          @click="selectedTopic = topic.key"
        >
          <strong>{{ topic.title }}</strong>
          <span>{{ topic.summary }}</span>
          <em>{{ topic.meetings }} 场会议 · {{ topic.knowledge }} 个知识</em>
        </button>
      </section>

      <section class="scene-panel story-panel">
        <div class="scene-kicker">项目会议 · 议题摘要</div>
        <h2>{{ activeTopic.title }}</h2>
        <p class="story-lead">{{ activeTopic.lead }} {{ templateText[selectedTemplate] }}</p>
        <div class="template-row" role="tablist" aria-label="议题表达模板">
          <button v-for="(label, key) in templateLabels" :key="key" type="button" :class="{ 'is-active': selectedTemplate === key }" @click="selectedTemplate = key as TemplateKey">{{ label }}</button>
        </div>
        <div class="story-content">
          <div v-for="story in activeTopic.stories" :key="story.time" class="story-line">
            <time>{{ story.time }}</time><span class="story-dot" />
            <div><strong>{{ story.title }}</strong><p>{{ story.copy }}</p><button type="button" class="scene-link">查看相关内容</button></div>
          </div>
        </div>
      </section>

      <aside class="scene-panel scene-detail">
        <div class="scene-kicker">当前议题</div>
        <h2>{{ activeTopic.title }}</h2>
        <p>{{ activeTopic.detail }}</p>
        <div class="meeting-card"><strong>你可以继续查看</strong><span>{{ activeTopic.decision }}</span><button type="button" class="scene-link">查看会议视频</button></div>
      </aside>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'

type SceneKey = 'training' | 'meeting'
type TemplateKey = 'evolution' | 'decision' | 'viewpoint' | 'progress' | 'dispute'
type TrainingRelationType = 'required_before' | 'recommended_before' | 'application' | 'complementary' | 'contrast'
type TrainingEdge = {
  key: string
  x1: number
  y1: number
  x2: number
  y2: number
  strong: boolean
  relationType: TrainingRelationType
  summary: string
}

const sceneOptions: Array<{ key: SceneKey; label: string }> = [
  { key: 'training', label: '培训学习' },
  { key: 'meeting', label: '项目会议' },
]
const scene = ref<SceneKey>('training')
const selectedCluster = ref('ai-learning')
const selectedPath = ref(-1)
const selectedRelation = ref<string | null>(null)
const selectedTopic = ref('segmentation')
const selectedTemplate = ref<TemplateKey>('evolution')

const relationTypeLabels: Record<TrainingRelationType, string> = {
  required_before: '必须先学',
  recommended_before: '推荐先学',
  application: '用于实践',
  complementary: '补充理解',
  contrast: '对照理解',
}
const trainingEdges: TrainingEdge[] = [
  { key: 'learning-to-knowledge', x1: 180, y1: 170, x2: 450, y2: 130, strong: true, relationType: 'recommended_before', summary: '先形成按需学习方法，再建立个人知识库，更容易确定资料的收集和整理边界。' },
  { key: 'knowledge-to-action', x1: 450, y1: 130, x2: 720, y2: 180, strong: true, relationType: 'application', summary: '把知识库中的内容用于真实任务，让整理结果进入行动、发布和反馈循环。' },
  { key: 'learning-to-humanities', x1: 180, y1: 170, x2: 290, y2: 355, strong: false, relationType: 'complementary', summary: '实践学习方法与人文判断相互补充，帮助学习者同时处理行动和认知边界。' },
  { key: 'knowledge-to-humanities', x1: 450, y1: 130, x2: 290, y2: 355, strong: false, relationType: 'contrast', summary: '工具化知识组织强调检索效率，人文理解更关注语境与判断，两者适合对照学习。' },
  { key: 'knowledge-to-classics', x1: 450, y1: 130, x2: 620, y2: 370, strong: false, relationType: 'recommended_before', summary: '先建立基础的知识组织方式，再梳理经典叙事中的人物、冲突与选择。' },
  { key: 'action-to-classics', x1: 720, y1: 180, x2: 620, y2: 370, strong: true, relationType: 'application', summary: '将行动与创造的方法用于经典叙事分析，把抽象理解转化为具体表达。' },
  { key: 'humanities-to-classics', x1: 290, y1: 355, x2: 620, y2: 370, strong: false, relationType: 'required_before', summary: '先理解人文判断的边界，再进入经典叙事中的人物选择与价值冲突。' },
]
const trainingClusters = [
  { key: 'ai-learning', title: 'AI 学习方法', category: '技能方法类', topics: 3, videos: 2, x: 20, y: 31, description: '围绕动手实践、碎片化输入和持续反馈，建立适应快速变化的 AI 学习方式。', path: [{ stage: '基本认识', title: '按需学习', explanation: '遇到真实问题时，再补需要的知识', meta: '概念 · 02:20-02:49', detail: '先创造真实场景和需求，再在解决问题时补充所需知识。' }, { stage: '操作方法', title: '用真实任务带动持续学习', explanation: '组合 2 个知识点 · 2 段视频片段', meta: '学习任务', detail: '把学习目标放进正在推进的工作任务中，边做边验证。' }, { stage: '示范案例', title: '发布 Vibe Coding 产品获得用户反馈', explanation: '把作品发布出去，用真实反馈发现下一步问题', meta: '案例 · 06:49-07:18', detail: '通过发布和反馈，把抽象方法转成可观察的结果。' }, { stage: '动手实践', title: 'AI 导师协作学习法', explanation: '让 AI 围绕自己的工作问题陪练并解释原理', meta: '方法论 · 07:55-08:55', detail: '描述工作场景和痛点，与 AI 协作完成工具并追问原理。' }, { stage: '复盘优化', title: '行动与反馈会生成学习动力', explanation: '回看行动反馈，把新问题带入下一轮学习', meta: '洞察 · 02:49-03:59', detail: '复盘行动结果，形成下一轮学习的真实问题。' }] },
  { key: 'ai-knowledge-base', title: 'AI 知识库与工具', category: '工具应用类', topics: 4, videos: 2, x: 50, y: 24, description: '从个人知识库到 AI Agent，把分散资料变成可检索、可协作的工作系统。', path: [{ stage: '基本认识', title: '建立个人知识库', explanation: '让重要信息有稳定的归档位置', meta: '概念 · 01:40-02:12', detail: '用统一结构保存资料，降低后续检索和复用成本。' }, { stage: '操作方法', title: '用 Obsidian 连接知识', explanation: '把笔记、任务与素材放在同一工作流', meta: '方法论 · 04:18-05:20', detail: '通过链接和标签形成可持续维护的个人知识网络。' }, { stage: '动手实践', title: '让 AI Agent 参与整理', explanation: '将重复整理交给 AI 协作完成', meta: '案例 · 06:10-07:02', detail: '明确输入、输出和复核标准，再把工作交给 Agent。' }] },
  { key: 'ai-action', title: 'AI 时代的行动与创造', category: '行动创造类', topics: 4, videos: 1, x: 80, y: 33, description: '从行动、发布到反馈，形成把想法快速变成成果的创造循环。', path: [{ stage: '基本认识', title: '先做再学', explanation: '用行动暴露真正的问题', meta: '洞察 · 01:12-02:03', detail: '先用真实行动验证方向，再补足知识缺口。' }, { stage: '示范案例', title: '把想法做成可用产品', explanation: '围绕具体用户需求快速迭代', meta: '案例 · 05:40-06:48', detail: '从最小可用版本开始，让用户反馈推动下一步。' }] },
  { key: 'humanities-cognition', title: '人文教育与认知边界', category: '认知理解类', topics: 4, videos: 3, x: 32, y: 70, description: '通过判断、理解和表达，重新认识人文学科在 AI 时代的价值。', path: [{ stage: '基本认识', title: '理解认知边界', explanation: '识别知识与判断之间的差异', meta: '概念 · 03:10-04:02', detail: '把信息、知识和判断放回具体的人与场景中理解。' }, { stage: '复盘优化', title: '用表达检验理解', explanation: '把复杂问题说清楚再行动', meta: '方法论 · 12:20-13:10', detail: '通过表达暴露理解中的缺口，推动下一轮思考。' }] },
  { key: 'classic-narrative', title: '经典叙事与人文理解', category: '内容理解类', topics: 3, videos: 1, x: 69, y: 72, description: '从经典叙事中提炼人物、冲突与选择，建立跨内容的理解线索。', path: [{ stage: '基本认识', title: '识别叙事结构', explanation: '从人物行动看见问题结构', meta: '概念 · 08:40-09:22', detail: '用人物、冲突和选择构建可复用的内容理解框架。' }, { stage: '示范案例', title: '把故事连接到现实问题', explanation: '用经典材料支持当下判断', meta: '案例 · 16:02-17:18', detail: '将故事中的选择映射到具体工作与生活场景。' }] },
]

const meetingTopics = [
  { key: 'segmentation', title: '用户分群', summary: '方案从行业划分转向使用场景', meetings: 4, knowledge: 7, lead: '这个议题在 4 场会议中持续讨论。方案从按行业划分，经过客户规模补充，最终改为按使用场景划分。', detail: '这个议题不是一条固定的会议流程，而是一组会议对同一个问题持续补充、取舍和验证。', decision: '最终方案按使用场景划分用户，销售沟通成本随之下降。', stories: [{ time: '08-12 · 10:00', title: '第一次提出：按行业划分', copy: '启动讨论认为行业标签最容易理解，但没有解释不同客户的使用差异。' }, { time: '08-18 · 14:20', title: '补充信息：加入客户规模', copy: '方案讨论补充了规模维度，但团队发现同一行业内仍然存在不同使用场景。' }, { time: '08-25 · 16:40', title: '形成当前方案：改按使用场景', copy: '评审会议选择场景维度，后续复盘显示销售沟通成本下降。' }] },
  { key: 'scope', title: '交付范围', summary: '从“全部做”收敛到核心链路', meetings: 3, knowledge: 5, lead: '交付范围在 3 场会议中逐步收敛，从覆盖所有需求转为优先保障核心链路。', detail: '团队把范围讨论拆成目标、约束和验证标准，让每次取舍都留下依据。', decision: '首期只交付核心链路，次要需求进入后续迭代。', stories: [{ time: '08-10 · 11:30', title: '提出完整交付设想', copy: '初版方案覆盖所有场景，但资源与时间约束尚未明确。' }, { time: '08-17 · 15:10', title: '识别核心链路', copy: '团队根据使用频率和业务价值重新排列优先级。' }, { time: '08-24 · 09:40', title: '收敛首期范围', copy: '评审确定首期只交付核心链路，并保留扩展接口。' }] },
  { key: 'budget', title: '预算约束', summary: '成本问题如何影响最终取舍', meetings: 2, knowledge: 4, lead: '预算约束让团队重新评估方案投入，最终把有限资源用于最能验证价值的部分。', detail: '预算不是单独的财务问题，而是影响交付顺序和验证方式的共同约束。', decision: '先验证高价值路径，再根据结果决定追加投入。', stories: [{ time: '08-15 · 13:20', title: '提出成本风险', copy: '基础设施和运营成本超出原始估算，需要重新评估投入方式。' }, { time: '08-22 · 16:00', title: '确定分阶段投入', copy: '会议决定先验证高价值路径，再根据结果追加预算。' }] },
]
const templateLabels: Record<TemplateKey, string> = { evolution: '议题演进', decision: '决策变化', viewpoint: '观点变化', progress: '事项推进', dispute: '争议澄清' }
const templateText: Record<TemplateKey, string> = { evolution: '以下按时间顺序呈现议题如何演进。', decision: '以下聚焦每次会议中的决策变化。', viewpoint: '以下突出不同参与者的观点变化。', progress: '以下按事项推进阶段组织内容。', dispute: '以下突出争议如何被澄清和验证。' }
const activeCluster = computed(() => trainingClusters.find(item => item.key === selectedCluster.value) || trainingClusters[0])
const activeRelation = computed(() => trainingEdges.find(item => item.key === selectedRelation.value) || null)
const activeTopic = computed(() => meetingTopics.find(item => item.key === selectedTopic.value) || meetingTopics[0])
const overviewStats = computed(() => scene.value === 'training' ? [{ label: '培训主题簇', value: '5' }, { label: '培训视频', value: '9' }, { label: '已提取知识', value: '43+' }, { label: '学习时长', value: '7.0 小时' }] : [{ label: '会议议题簇', value: '3' }, { label: '会议视频', value: '9' }, { label: '知识', value: String(activeTopic.value.knowledge) }, { label: '会议时长', value: '7.2 小时' }])
function selectCluster(key: string) { selectedCluster.value = key; selectedRelation.value = null; selectedPath.value = -1 }
function selectRelation(key: string) { selectedRelation.value = key; selectedPath.value = -1 }
function refresh() {
  if (scene.value === 'training') {
    selectedCluster.value = 'ai-learning'
    selectedRelation.value = null
    selectedPath.value = -1
    return
  }
  selectedTopic.value = 'segmentation'
  selectedTemplate.value = 'evolution'
}
defineExpose({ refresh })
watch(scene, () => { selectedRelation.value = null; selectedPath.value = -1 })
</script>

<style scoped>
.scene-view { display: grid; gap: calc(var(--td-comp-margin-s) * 1.5); min-width: 0; }
.scene-view__toolbar { display: flex; align-items: center; justify-content: space-between; gap: 14px; }
.scene-view__switch { display: inline-flex; gap: 3px; padding: 3px; border: 1px solid rgba(255,255,255,.86); border-radius: var(--td-radius-medium); background: rgba(226,233,229,.54); }
.scene-view__switch button { min-height: 32px; padding: 4px 14px; border: 0; border-radius: var(--td-radius-small); color: var(--td-text-color-secondary); background: transparent; cursor: pointer; font-size: var(--td-font-size-body-small); }
.scene-view__switch button:hover, .scene-view__switch button.is-active { color: var(--td-brand-color); background: rgba(255,255,255,.9); font-weight: 600; }
.scene-view__hint { color: var(--td-text-color-secondary); font-size: var(--td-font-size-body-small); }
.scene-view__overview { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: var(--td-comp-margin-s); }
.scene-stat, .scene-panel { border: 1px solid rgba(255,255,255,.84); background: rgba(255,255,255,.45); backdrop-filter: blur(24px) saturate(112%); -webkit-backdrop-filter: blur(24px) saturate(112%); }
.scene-stat { min-width: 0; padding: 14px 16px; border-radius: var(--td-radius-medium); }
.scene-stat span { display: block; color: var(--td-text-color-secondary); font-size: var(--td-font-size-body-small); }
.scene-stat strong { display: block; margin-top: 7px; font-size: 24px; font-weight: 500; line-height: 28px; }
.training-layout, .meeting-layout { display: grid; grid-template-columns: minmax(0, 1fr) 360px; gap: calc(var(--td-comp-margin-s) * 2); align-items: start; }
.meeting-layout { grid-template-columns: 260px minmax(0, 1fr) 330px; }
.scene-panel { min-width: 0; border-radius: var(--td-radius-extraLarge); }
.scene-panel__head { display: flex; align-items: center; justify-content: space-between; gap: 12px; min-height: 52px; padding: 0 18px; border-bottom: 1px solid color-mix(in srgb, var(--td-component-stroke) 72%, transparent); }
.scene-panel__head strong { font-size: var(--td-font-size-title-small); }
.scene-panel__head span { color: var(--td-text-color-secondary); font-size: 11px; }
.topic-network { position: relative; min-height: 540px; overflow: hidden; background: radial-gradient(circle, rgba(52,79,65,.11) 1px, transparent 1.5px), rgba(255,255,255,.24); background-size: 28px 28px; }
.topic-network svg { position: absolute; inset: 0; width: 100%; height: 100%; }
.topic-edge { cursor: pointer; outline: none; }
.topic-edge__hit { stroke: transparent; stroke-width: 18; pointer-events: stroke; }
.topic-edge__line { stroke: color-mix(in srgb, var(--td-text-color-secondary) 38%, transparent); stroke-width: 1.5; pointer-events: none; transition: stroke .16s ease, stroke-width .16s ease; }
.topic-edge.is-strong .topic-edge__line { stroke-width: 2; }
.topic-edge:hover .topic-edge__line, .topic-edge:focus-visible .topic-edge__line, .topic-edge.is-selected .topic-edge__line { stroke: var(--td-brand-color); stroke-width: 3; }
.cluster-node { position: absolute; z-index: 1; width: 176px; min-height: 92px; padding: 14px 14px 12px 18px; transform: translate(-50%, -50%); border: 1px solid rgba(255,255,255,.92); border-radius: var(--td-radius-medium); color: var(--td-text-color-primary); background: rgba(255,255,255,.92); box-shadow: var(--td-shadow-1); cursor: pointer; text-align: left; transition: transform .16s ease, box-shadow .16s ease, border-color .16s ease; }
.cluster-node::before { position: absolute; top: 0; bottom: 0; left: 0; width: 4px; border-radius: var(--td-radius-medium) 0 0 var(--td-radius-medium); background: var(--td-brand-color); content: ''; }
.cluster-node:hover, .cluster-node.is-selected { transform: translate(-50%, -50%) scale(1.03); border-color: color-mix(in srgb, var(--td-brand-color) 48%, transparent); box-shadow: var(--td-shadow-2); }
.cluster-node strong { display: block; font-size: var(--td-font-size-body-medium); font-weight: 600; line-height: 17px; }
.cluster-node span { display: block; margin-top: 7px; color: var(--td-text-color-secondary); font-size: 10px; line-height: 15px; }
.topic-network__caption { position: absolute; right: 18px; bottom: 16px; left: 18px; padding: 10px 12px; border: 1px solid rgba(255,255,255,.84); border-radius: var(--td-radius-medium); color: var(--td-text-color-secondary); background: rgba(255,255,255,.72); font-size: 11px; line-height: 16px; }
.training-detail, .scene-detail, .story-panel { padding: 18px; }
.scene-kicker { color: var(--td-brand-color); font-size: 11px; font-weight: 600; }
.training-detail h2, .scene-detail h2, .story-panel h2 { margin: 5px 0 8px; font-size: var(--td-font-size-title-large); font-weight: 600; line-height: 26px; }
.training-detail__description, .scene-detail > p, .story-lead { margin: 0; color: var(--td-text-color-secondary); font-size: var(--td-font-size-body-small); line-height: 19px; }
.relation-detail__summary { margin-top: 14px; padding-top: 14px; border-top: 1px solid color-mix(in srgb, var(--td-component-stroke) 72%, transparent); }
.scene-section { margin-top: 18px; padding-top: 16px; border-top: 1px solid color-mix(in srgb, var(--td-component-stroke) 72%, transparent); }
.scene-section__label { display: block; margin-bottom: 8px; color: var(--td-text-color-secondary); font-size: 11px; }
.training-path { display: grid; grid-template-columns: 1fr; gap: 8px; }
.path-step { position: relative; display: block; width: 100%; min-height: 0; padding: 12px; border: 1px solid rgba(255,255,255,.88); border-radius: var(--td-radius-medium); color: inherit; background: rgba(255,255,255,.62); cursor: pointer; text-align: left; }
.path-step:hover, .path-step.is-active { border-color: color-mix(in srgb, var(--td-brand-color) 36%, transparent); background: color-mix(in srgb, var(--td-brand-color-light) 48%, rgba(255,255,255,.8)); }
.path-step small, .path-step em { display: block; color: var(--td-text-color-secondary); font-size: 10px; font-style: normal; }
.path-step strong { display: block; margin-top: 7px; font-size: 12px; line-height: 17px; }
.path-step span { display: block; margin-top: 5px; color: var(--td-text-color-secondary); font-size: 10px; line-height: 15px; }
.path-step em { margin-top: 5px; }
.path-detail { margin-top: 10px; padding: 13px; border: 1px solid color-mix(in srgb, var(--td-brand-color) 24%, transparent); border-radius: var(--td-radius-medium); background: color-mix(in srgb, var(--td-brand-color-light) 34%, rgba(255,255,255,.82)); }
.path-detail span { display: block; margin-bottom: 7px; color: var(--td-text-color-secondary); font-size: 10px; }
.path-detail strong { font-size: 12px; }
.path-detail p { margin: 5px 0 0; color: var(--td-text-color-secondary); font-size: 11px; line-height: 17px; }
.scene-link { margin-top: 7px; padding: 0; border: 0; color: var(--td-brand-color); background: transparent; cursor: pointer; font-size: 11px; }
.topic-list { padding: 10px; }
.topic-item { display: block; width: 100%; margin: 0 0 4px; padding: 12px; border: 1px solid transparent; border-radius: var(--td-radius-medium); background: transparent; cursor: pointer; text-align: left; }
.topic-item:hover, .topic-item.is-active { border-color: color-mix(in srgb, var(--td-brand-color) 18%, transparent); background: color-mix(in srgb, var(--td-brand-color-light) 55%, transparent); }
.topic-item strong, .topic-item span, .topic-item em { display: block; }
.topic-item strong { font-size: 12px; font-weight: 600; }
.topic-item span { margin-top: 4px; color: var(--td-text-color-secondary); font-size: 11px; line-height: 16px; }
.topic-item em { margin-top: 8px; color: var(--td-text-color-secondary); font-size: 10px; font-style: normal; }
.template-row { display: flex; flex-wrap: wrap; gap: 6px; margin: 16px 0; }
.template-row button { min-height: 30px; padding: 4px 10px; border: 1px solid transparent; border-radius: var(--td-radius-round); color: var(--td-text-color-secondary); background: rgba(255,255,255,.65); cursor: pointer; font-size: 11px; }
.template-row button:hover, .template-row button.is-active { border-color: color-mix(in srgb, var(--td-brand-color) 20%, transparent); color: var(--td-brand-color); background: var(--td-brand-color-light); }
.story-content { margin-top: 4px; }
.story-line { display: grid; grid-template-columns: 74px 14px minmax(0, 1fr); gap: 10px; align-items: start; margin-top: 14px; }
.story-line time { padding-top: 3px; color: var(--td-text-color-secondary); font: 10px ui-monospace, SFMono-Regular, Menlo, monospace; }
.story-dot { position: relative; width: 12px; height: 12px; margin-top: 4px; border: 3px solid color-mix(in srgb, var(--td-brand-color) 30%, transparent); border-radius: var(--td-radius-circle); background: var(--td-brand-color); }
.story-line:not(:last-child) .story-dot::after { position: absolute; top: 10px; left: 3px; width: 1px; height: 58px; background: color-mix(in srgb, var(--td-brand-color) 28%, transparent); content: ''; }
.story-line strong { display: block; font-size: 12px; font-weight: 600; }
.story-line p { margin: 4px 0 0; color: var(--td-text-color-secondary); font-size: 11px; line-height: 17px; }
.meeting-card { margin-top: 14px; padding: 12px; border: 1px solid color-mix(in srgb, var(--td-component-stroke) 72%, transparent); border-radius: var(--td-radius-medium); background: rgba(255,255,255,.46); }
.meeting-card strong, .meeting-card span { display: block; }
.meeting-card strong { font-size: 12px; font-weight: 600; }
.meeting-card span { margin-top: 4px; color: var(--td-text-color-secondary); font-size: 11px; line-height: 17px; }
@media (max-width: 1120px) { .meeting-layout { grid-template-columns: 1fr; }.topic-list { display: flex; gap: 6px; overflow-x: auto; padding: 0 0 4px; }.topic-item { min-width: 190px; margin: 0; } }
@media (max-width: 820px) { .scene-view__toolbar { align-items: flex-start; flex-direction: column; }.scene-view__hint { display: none; }.training-layout, .meeting-layout { grid-template-columns: 1fr; }.topic-network { min-height: 500px; }.scene-view__overview { grid-template-columns: repeat(2, minmax(0, 1fr)); } }
@media (max-width: 520px) { .cluster-node { width: 148px; }.topic-network__caption { right: 10px; bottom: 10px; left: 10px; }.story-line { grid-template-columns: 64px 14px minmax(0, 1fr); }.scene-stat { padding: 12px; }.scene-stat strong { font-size: 20px; } }
</style>
