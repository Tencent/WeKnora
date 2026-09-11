<template>
  <section class="meeting-scene" aria-label="会议场景">
    <div class="meeting-demo-note">
      <span class="meeting-demo-note__dot"></span>
      当前为会议场景展示样例 · 主题簇将按当前账号上传的视频实时生成
    </div>

    <div class="meeting-overview" aria-label="会议场景概览">
      <article v-for="stat in overviewStats" :key="stat.label" class="meeting-stat">
        <span>{{ stat.label }}</span>
        <strong :class="{ 'is-warning': stat.warning }">{{ stat.value }}</strong>
        <small>{{ stat.note }}</small>
      </article>
    </div>

    <div class="meeting-content">
      <section class="meeting-panel meeting-relations" aria-label="会议主题簇关系">
        <header class="meeting-panel__head">
          <div>
            <strong>会议主题簇</strong>
            <span>{{ clusters.length }} 个主题 · {{ meetingCount }} 场视频</span>
          </div>
          <button type="button" class="meeting-icon-button" title="重新分析会议主题" @click="notify('主题簇将在会议场景接口接入后重新分析')">
            <RefreshIcon />
          </button>
        </header>

        <div class="relation-board">
          <svg class="relation-board__lines" viewBox="0 0 620 420" preserveAspectRatio="none" aria-hidden="true">
            <defs>
              <marker id="meeting-arrow" markerWidth="8" markerHeight="8" refX="7" refY="4" orient="auto">
                <path d="M0,0 L8,4 L0,8 z" fill="rgba(0,0,0,.34)" />
              </marker>
            </defs>
            <path d="M180 120 C260 120 300 92 398 92" marker-end="url(#meeting-arrow)" />
            <path d="M180 120 C260 170 300 206 398 206" marker-end="url(#meeting-arrow)" />
            <path d="M180 306 C260 286 300 236 398 206" marker-end="url(#meeting-arrow)" />
            <path d="M180 306 C270 350 315 344 398 326" marker-end="url(#meeting-arrow)" />
            <path class="is-dashed" d="M480 142 C525 168 525 272 480 292" />
          </svg>
          <button
            v-for="cluster in clusters"
            :key="cluster.id"
            type="button"
            class="relation-node"
            :class="{ 'is-selected': selectedClusterId === cluster.id }"
            :style="{ left: cluster.x + '%', top: cluster.y + '%' }"
            @click="selectCluster(cluster.id)"
          >
            <strong>{{ cluster.title }}</strong>
            <span>{{ cluster.videos }} 场视频 · {{ cluster.decisions }} 项决策</span>
          </button>
          <div class="relation-legend">
            <span><i class="is-solid"></i>决策依赖</span>
            <span><i class="is-dashed"></i>共同业务对象</span>
          </div>
        </div>
      </section>

      <aside v-if="activeCluster" class="meeting-panel meeting-detail" aria-live="polite">
        <header class="meeting-detail__head">
          <div>
            <span class="meeting-kicker">会议主题</span>
            <h2>{{ activeCluster.title }}</h2>
          </div>
          <span class="meeting-count-pill">{{ activeCluster.videos }} 场会议</span>
        </header>
        <p class="meeting-summary">{{ activeCluster.summary }}</p>

        <nav class="meeting-detail__tabs" role="tablist" aria-label="会议主题详情">
          <button v-for="tab in detailTabs" :key="tab.id" type="button" role="tab" :aria-selected="activeTab === tab.id" :class="{ 'is-active': activeTab === tab.id }" @click="activeTab = tab.id">
            {{ tab.label }}<small>{{ tab.count }}</small>
          </button>
        </nav>

        <div class="meeting-detail__body">
          <template v-if="activeTab === 'evolution'">
            <div class="detail-section-head"><strong>会议演变</strong><span>按会议时间排序</span></div>
            <div class="evolution-list">
              <article v-for="event in activeCluster.evolution" :key="event.id" class="evolution-item">
                <div class="evolution-marker"><span></span><time>{{ event.date }}</time></div>
                <div class="evolution-card">
                  <div class="evolution-card__top">
                    <strong>{{ event.meeting }}</strong>
                    <span class="evolution-change-tag" :class="event.changeType">{{ event.changeLabel }}</span>
                  </div>
                  <p class="evolution-summary">{{ event.summary }}</p>
                  <div class="evolution-change">
                    <span>关键变化</span>
                    <strong>{{ event.change }}</strong>
                  </div>
                  <div class="evolution-result">
                    <span>会议结果</span>
                    <strong>{{ event.result }}</strong>
                  </div>
                  <div class="evolution-context">
                    <span>{{ event.attendees }}</span>
                    <span>{{ event.duration }}</span>
                    <span v-if="event.actionCount">行动项 {{ event.actionCount }}</span>
                  </div>
                  <button type="button" class="evolution-video-link" @click="openVideo(event)"><PlayCircleIcon />查看视频智能总结</button>
                </div>
              </article>
            </div>
          </template>

          <template v-else-if="activeTab === 'todos'">
            <div class="detail-section-head"><strong>待办</strong><span>按视频归集 · {{ activeCluster.todos.length }} 项</span></div>
            <div class="todo-list">
              <article v-for="todo in activeCluster.todos" :key="todo.id" class="todo-item">
                <span class="todo-status" :class="todo.status"></span>
                <div class="todo-content">
                  <strong>{{ todo.title }}</strong>
                  <p>{{ todo.meeting }} · {{ todo.owner }} · {{ todo.due }}</p>
                  <blockquote class="todo-evidence">
                    <span>原文证据 · {{ todo.timeRange }}</span>
                    <p>“{{ todo.evidenceQuote }}”</p>
                    <button type="button" @click="openTodoEvidence(todo)"><PlayCircleIcon />定位原视频</button>
                  </blockquote>
                </div>
              </article>
            </div>
          </template>

          <template v-else>
            <div class="detail-section-head"><strong>相关知识</strong><span>按知识对象归集 · {{ activeCluster.knowledge.length }} 项</span></div>
            <div class="knowledge-list">
              <button v-for="knowledge in activeCluster.knowledge" :key="knowledge.id" type="button" class="knowledge-item" @click="notify('将打开知识 Wiki：' + knowledge.title)">
                <span class="knowledge-type" :class="knowledge.type">{{ knowledge.typeLabel }}</span>
                <span><strong>{{ knowledge.title }}</strong><small>{{ knowledge.note }}</small></span>
                <ChevronRightIcon />
              </button>
            </div>
          </template>
        </div>
      </aside>
    </div>

    <div v-if="toast" class="meeting-toast" role="status">{{ toast }}</div>
  </section>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { ChevronRightIcon, PlayCircleIcon, RefreshIcon } from 'tdesign-icons-vue-next'

const emit = defineEmits<{
  selectVideo: [videoId: string, seconds: number]
}>()

type DetailTab = 'evolution' | 'todos' | 'knowledge'

const activeTab = ref<DetailTab>('evolution')
const selectedClusterId = ref('product')
const toast = ref('')
let toastTimer: number | undefined

const clusters = [
  {
    id: 'product', title: '薪享贷产品方案', x: 29, y: 28, videos: 3, decisions: 2,
    summary: '围绕目标客户、授信额度和首期利率确定产品边界。额度方案在风险评审后从 30 万元调整为 20 万元，利率仍待进一步测算。',
    evolution: [
      { id: 'p1', date: '09/03', meeting: '薪享贷产品立项会', duration: '52 分钟', attendees: '产品、渠道、项目组 · 6 人', summary: '确定首期目标客户并提出额度与利率初始方案。', changeType: 'new', changeLabel: '新增', change: '首次提出首期授信上限 30 万元', result: '形成初始产品方案，提交风险评审', actionCount: 1, videoId: 'demo-product-0903', seconds: 720 },
      { id: 'p2', date: '09/10', meeting: '风险与合规评审会', duration: '64 分钟', attendees: '产品、风险、合规 · 8 人', summary: '风险团队认为高额度客户历史样本不足，需要收紧首期风险边界。', changeType: 'adjusted', changeLabel: '调整', change: '授信上限建议由 30 万元下调至 20 万元', result: '额度尚未最终确认；利率形成 3.6% 与 4.1% 分歧', actionCount: 2, videoId: 'demo-risk-0910', seconds: 1938 },
      { id: 'p3', date: '09/17', meeting: '薪享贷上线准备会', duration: '47 分钟', attendees: '项目、风险、渠道 · 7 人', summary: '综合风险意见确认首期额度边界，并补充高额度申请的审核措施。', changeType: 'confirmed', changeLabel: '确认', change: '接受 20 万元上限，新增 10 万元人工审核门槛', result: '最近一次明确决策：首期上限 20 万元', actionCount: 1, videoId: 'demo-launch-0917', seconds: 1600 },
    ],
    todos: [
      { id: 't1', title: '补充 3.6% 与 4.1% 利率的盈亏测算', meeting: '风险与合规评审会', owner: '财务部、风险部', due: '截止 09/22', status: 'pending', evidenceQuote: '财务和风险这周把 3.6% 和 4.1% 两个方案的盈亏测算补齐，我们下次会上把利率定下来。', timeRange: '41:12–41:55', videoId: 'demo-risk-0910', seconds: 2512 },
      { id: 't2', title: '完成 10 万元以上人工审核规则配置', meeting: '薪享贷上线准备会', owner: '风险策略组', due: '截止 09/24', status: 'progress', evidenceQuote: '风险策略组在二十四号之前把十万以上转人工的规则配好，联调通过以后再进发布清单。', timeRange: '27:16–27:42', videoId: 'demo-launch-0917', seconds: 1636 },
    ],
    knowledge: [
      { id: 'k1', title: '薪享贷', type: 'entity', typeLabel: '实体', note: '产品 · 3 场会议' },
      { id: 'k2', title: '分阶段额度策略', type: 'method', typeLabel: '方法', note: '方法 · 2 场会议' },
      { id: 'k3', title: '高额度客群样本不足', type: 'case', typeLabel: '案例', note: '案例 · 风险评审会' },
      { id: 'k4', title: '人工审核门槛', type: 'concept', typeLabel: '概念', note: '概念 · 上线准备会' },
    ],
  },
  {
    id: 'risk', title: '风险策略', x: 29, y: 73, videos: 4, decisions: 3,
    summary: '聚合额度、利率和审核规则的风险讨论，是产品方案进入上线阶段的前置条件。',
    evolution: [
      { id: 'r1', date: '09/10', meeting: '风险与合规评审会', duration: '64 分钟', attendees: '风险、合规、产品 · 8 人', summary: '依据有限的高额度样本，风险团队提出首期控制建议。', changeType: 'new', changeLabel: '新增', change: '提出 20 万元上限和 10 万元人工审核门槛', result: '形成风险建议，等待项目组确认', actionCount: 2, videoId: 'demo-risk-0910', seconds: 1938 },
      { id: 'r2', date: '09/17', meeting: '薪享贷上线准备会', duration: '47 分钟', attendees: '项目、风险、渠道 · 7 人', summary: '项目组接受额度控制方向，并将风控规则列入上线检查。', changeType: 'confirmed', changeLabel: '确认', change: '风险建议由评审意见转为上线前置规则', result: '规则配置完成后才可正式上线', actionCount: 1, videoId: 'demo-launch-0917', seconds: 1600 },
    ],
    todos: [
      { id: 'r-t1', title: '完成风控规则联调', meeting: '薪享贷上线准备会', owner: '风险策略组', due: '截止 09/24', status: 'progress', evidenceQuote: '风险规则配置完以后，二十四号安排一轮完整联调，没有阻断问题再确认上线。', timeRange: '29:08–29:31', videoId: 'demo-launch-0917', seconds: 1748 },
      { id: 'r-t2', title: '补充利率盈亏测算', meeting: '风险与合规评审会', owner: '财务部、风险部', due: '截止 09/22', status: 'pending', evidenceQuote: '财务和风险把两档利率的收入、预期损失和运营成本再算一遍，九月二十二号前给结果。', timeRange: '42:02–42:28', videoId: 'demo-risk-0910', seconds: 2522 },
    ],
    knowledge: [
      { id: 'r-k1', title: '预期损失', type: 'concept', typeLabel: '概念', note: '概念 · 风险评审会' },
      { id: 'r-k2', title: '风险策略组', type: 'entity', typeLabel: '实体', note: '机构 · 4 场会议' },
      { id: 'r-k3', title: '高额度客群样本不足', type: 'case', typeLabel: '案例', note: '案例 · 风险评审会' },
    ],
  },
  {
    id: 'launch', title: '上线准备', x: 71, y: 22, videos: 2, decisions: 2,
    summary: '围绕发布时间、试点城市和上线前置条件推进落地，正式上线日期已由 10 月 15 日调整至 11 月 1 日。',
    evolution: [
      { id: 'l1', date: '09/03', meeting: '薪享贷产品立项会', duration: '52 分钟', attendees: '项目、产品 · 6 人', summary: '按照原开发和评审周期确定首版项目排期。', changeType: 'new', changeLabel: '新增', change: '首次提出 10 月 15 日正式上线', result: '形成初始上线计划', actionCount: 1, videoId: 'demo-product-0903', seconds: 2596 },
      { id: 'l2', date: '09/17', meeting: '薪享贷上线准备会', duration: '47 分钟', attendees: '项目、风险、渠道 · 7 人', summary: '风控规则联调和协议审核仍需两周，原排期不再可行。', changeType: 'adjusted', changeLabel: '调整', change: '正式上线日期由 10 月 15 日顺延至 11 月 1 日', result: '新上线日期已明确，三地试点同步调整', actionCount: 1, videoId: 'demo-launch-0917', seconds: 502 },
    ],
    todos: [
      { id: 'l-t1', title: '更新 11 月 1 日发布排期', meeting: '薪享贷上线准备会', owner: '项目经理', due: '截止 09/19', status: 'done', evidenceQuote: '项目经理今天把排期改到十一月一号，开发、审核和三个试点分行的时间都一起往后调整。', timeRange: '09:03–09:26', videoId: 'demo-launch-0917', seconds: 543 },
    ],
    knowledge: [
      { id: 'l-k1', title: '发布计划', type: 'method', typeLabel: '方法', note: '方法 · 上线准备会' },
      { id: 'l-k2', title: '上线日期调整', type: 'insight', typeLabel: '洞察', note: '洞察 · 2 场会议' },
    ],
  },
  {
    id: 'compliance', title: '合规政策', x: 71, y: 78, videos: 3, decisions: 1,
    summary: '汇总协议审核、客户准入和试点范围等合规约束，为产品和上线准备提供边界。',
    evolution: [
      { id: 'c1', date: '09/10', meeting: '风险与合规评审会', duration: '64 分钟', attendees: '合规、风险、产品 · 8 人', summary: '合规团队核对客户准入范围和产品协议准备情况。', changeType: 'new', changeLabel: '新增', change: '明确代发满 6 个月的准入要求和协议审核要求', result: '形成合规边界，协议仍需完成审核', actionCount: 1, videoId: 'demo-risk-0910', seconds: 2500 },
      { id: 'c2', date: '09/17', meeting: '薪享贷上线准备会', duration: '47 分钟', attendees: '项目、风险、渠道 · 7 人', summary: '上线检查发现协议审核尚未结束，需要继续执行原合规要求。', changeType: 'continued', changeLabel: '延续', change: '准入范围不变，协议审核要求继续有效', result: '协议审核完成前不得正式上线', actionCount: 1, videoId: 'demo-launch-0917', seconds: 840 },
    ],
    todos: [
      { id: 'c-t1', title: '完成客户协议审核', meeting: '薪享贷上线准备会', owner: '合规部', due: '截止 09/27', status: 'pending', evidenceQuote: '客户协议请合规部在二十七号之前完成终审，协议没有通过就不能进入正式发布。', timeRange: '14:01–14:24', videoId: 'demo-launch-0917', seconds: 841 },
    ],
    knowledge: [
      { id: 'c-k1', title: '代发客户满 6 个月', type: 'concept', typeLabel: '概念', note: '概念 · 产品立项会' },
      { id: 'c-k2', title: '客户协议审核', type: 'method', typeLabel: '方法', note: '方法 · 合规评审会' },
    ],
  },
]

const activeCluster = computed(() => clusters.find(cluster => cluster.id === selectedClusterId.value) || clusters[0])
const meetingCount = computed(() => new Set(clusters.flatMap(cluster => cluster.evolution.map(event => event.videoId))).size)
const overviewStats = computed(() => [
  { label: '会议主题', value: String(clusters.length), note: '已形成主题簇' },
  { label: '会议视频', value: String(meetingCount.value), note: '当前账号上传' },
  { label: '重要决策', value: '8', note: '来自全部会议' },
  { label: '待办', value: '3', note: '待决策 1 · 待行动 2', warning: true },
])
const detailTabs = computed(() => [
  { id: 'evolution' as DetailTab, label: '会议演变', count: activeCluster.value.evolution.length },
  { id: 'todos' as DetailTab, label: '待办', count: activeCluster.value.todos.length },
  { id: 'knowledge' as DetailTab, label: '相关知识', count: activeCluster.value.knowledge.length },
])

function selectCluster(id: string) {
  selectedClusterId.value = id
  activeTab.value = 'evolution'
}
function openVideo(event: { videoId: string; seconds: number }) {
  emit('selectVideo', event.videoId, event.seconds)
}
function openTodoEvidence(todo: { videoId: string; seconds: number }) {
  emit('selectVideo', todo.videoId, todo.seconds)
}
function notify(message: string) {
  toast.value = message
  window.clearTimeout(toastTimer)
  toastTimer = window.setTimeout(() => { toast.value = '' }, 2400)
}
</script>

<style scoped>
.meeting-scene { display: grid; gap: calc(var(--td-comp-margin-s) * 1.5); min-width: 0; }
.meeting-demo-note { display: flex; align-items: center; gap: 7px; min-height: 30px; padding: 0 11px; border: 1px solid rgba(255,255,255,.84); border-radius: var(--td-radius-medium); color: var(--td-text-color-secondary); background: rgba(255,255,255,.36); font-size: var(--td-font-size-body-small); }
.meeting-demo-note__dot { width: 7px; height: 7px; border-radius: 50%; background: var(--td-warning-color); box-shadow: 0 0 0 3px var(--td-warning-color-light); }
.meeting-overview { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: var(--td-comp-margin-s); }
.meeting-stat, .meeting-panel { border: 1px solid rgba(255,255,255,.84); background: rgba(255,255,255,.45); backdrop-filter: blur(24px) saturate(112%); -webkit-backdrop-filter: blur(24px) saturate(112%); }
.meeting-stat { display: grid; min-width: 0; min-height: 104px; padding: 14px 16px; border-radius: var(--td-radius-medium); }
.meeting-stat span, .meeting-stat small { color: var(--td-text-color-secondary); font-size: var(--td-font-size-body-small); }
.meeting-stat strong { margin-top: 6px; font-size: 28px; line-height: 32px; font-weight: 600; }
.meeting-stat strong.is-warning { color: var(--td-warning-color); }
.meeting-stat small { margin-top: 3px; color: var(--td-text-color-placeholder); font-size: 11px; }
.meeting-content { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: calc(var(--td-comp-margin-s) * 2); min-height: 0; }
.meeting-panel { min-width: 0; border-radius: var(--td-radius-extraLarge); overflow: hidden; }
.meeting-panel__head { display: flex; align-items: center; justify-content: space-between; gap: 12px; min-height: 58px; padding: 0 18px; border-bottom: 1px solid color-mix(in srgb, var(--td-component-stroke) 72%, transparent); }
.meeting-panel__head > div { display: grid; gap: 3px; }
.meeting-panel__head strong { font-size: var(--td-font-size-title-small); }
.meeting-panel__head span { color: var(--td-text-color-secondary); font-size: 11px; }
.meeting-icon-button { display: grid; width: 30px; height: 30px; place-items: center; border: 1px solid rgba(0,0,0,.08); border-radius: var(--td-radius-medium); color: var(--td-text-color-secondary); background: rgba(255,255,255,.55); cursor: pointer; }
.meeting-icon-button:hover { color: var(--td-brand-color); border-color: rgba(7,192,95,.28); }
.meeting-icon-button :deep(svg) { width: 16px; height: 16px; }
.relation-board { position: relative; min-height: 470px; overflow: hidden; background: radial-gradient(circle, rgba(52,79,65,.11) 1px, transparent 1.5px), rgba(255,255,255,.24); background-size: 28px 28px; }
.relation-board__lines { position: absolute; inset: 0; width: 100%; height: 100%; }
.relation-board__lines path { fill: none; stroke: rgba(0,0,0,.22); stroke-width: 1.5; }
.relation-board__lines .is-dashed { stroke-dasharray: 6 6; stroke: rgba(0,0,0,.17); }
.relation-node { position: absolute; z-index: 1; width: 164px; min-height: 86px; padding: 13px 14px 11px 17px; transform: translate(-50%, -50%); overflow: hidden; border: 1px solid rgba(255,255,255,.94); border-radius: var(--td-radius-medium); color: var(--td-text-color-primary); background: rgba(255,255,255,.9); box-shadow: var(--td-shadow-1); cursor: pointer; text-align: left; transition: .16s ease; }
.relation-node::before { position: absolute; inset: 0 auto 0 0; width: 4px; border-radius: var(--td-radius-medium) 0 0 var(--td-radius-medium); background: var(--td-brand-color); content: ''; }
.relation-node:hover, .relation-node.is-selected { transform: translate(-50%, -50%) scale(1.025); border-color: rgba(7,192,95,.38); box-shadow: var(--td-shadow-2); }
.relation-node strong { display: -webkit-box; overflow: hidden; font-size: 14px; line-height: 19px; -webkit-box-orient: vertical; -webkit-line-clamp: 2; }
.relation-node span { display: block; margin-top: 8px; color: var(--td-text-color-secondary); font-size: 11px; }
.relation-legend { position: absolute; right: 14px; bottom: 13px; left: 14px; display: flex; gap: 14px; color: var(--td-text-color-placeholder); font-size: 10px; }
.relation-legend span { display: inline-flex; align-items: center; gap: 5px; }
.relation-legend i { display: inline-block; width: 20px; height: 1px; background: rgba(0,0,0,.34); }
.relation-legend i.is-dashed { background: repeating-linear-gradient(90deg, rgba(0,0,0,.34) 0 4px, transparent 4px 7px); }
.meeting-detail { display: flex; flex-direction: column; }
.meeting-detail__head { display: flex; align-items: flex-start; justify-content: space-between; gap: 10px; padding: 18px 18px 0; }
.meeting-kicker { color: var(--td-brand-color); font-size: 11px; font-weight: 600; }
.meeting-detail h2 { margin: 5px 0 0; overflow-wrap: anywhere; font-size: var(--td-font-size-title-large); line-height: 26px; }
.meeting-count-pill { flex: none; padding: 4px 8px; border-radius: 999px; color: var(--td-text-color-secondary); background: var(--td-bg-color-secondarycontainer); font-size: 10px; }
.meeting-summary { margin: 10px 18px 0; color: var(--td-text-color-secondary); font-size: var(--td-font-size-body-small); line-height: 20px; }
.meeting-detail__tabs { display: flex; gap: 22px; min-height: 48px; margin-top: 14px; padding: 0 18px; border-bottom: 1px solid rgba(0,0,0,.08); }
.meeting-detail__tabs button { position: relative; display: flex; align-items: center; gap: 4px; padding: 0; border: 0; color: var(--td-text-color-secondary); background: transparent; cursor: pointer; font-size: 12px; }
.meeting-detail__tabs button.is-active { color: var(--td-brand-color); font-weight: 600; }
.meeting-detail__tabs button.is-active::after { position: absolute; right: 0; bottom: -1px; left: 0; height: 2px; border-radius: 2px 2px 0 0; background: var(--td-brand-color); content: ''; }
.meeting-detail__tabs small { color: var(--td-text-color-placeholder); font-size: 10px; font-weight: 400; }
.meeting-detail__body { min-height: 0; padding: 15px 18px 20px; overflow: auto; }
.detail-section-head { display: flex; align-items: center; justify-content: space-between; gap: 10px; margin-bottom: 9px; }
.detail-section-head strong { font-size: 13px; }
.detail-section-head span { color: var(--td-text-color-placeholder); font-size: 10px; }
.evolution-list { position: relative; display: grid; gap: 12px; }
.evolution-list::before { position: absolute; top: 9px; bottom: 12px; left: 35px; width: 1px; background: rgba(0,0,0,.14); content: ''; }
.evolution-item { position: relative; display: grid; grid-template-columns: 71px minmax(0, 1fr); gap: 10px; }
.evolution-marker { position: relative; z-index: 1; display: grid; align-content: start; gap: 5px; }
.evolution-marker span { width: 12px; height: 12px; margin-left: 29px; border: 2px solid white; border-radius: 50%; background: #a6abb0; box-shadow: 0 0 0 1px rgba(0,0,0,.13); }
.evolution-item:last-child .evolution-marker span { background: var(--td-brand-color); box-shadow: 0 0 0 3px var(--td-brand-color-light); }
.evolution-marker time { color: var(--td-text-color-placeholder); font-size: 11px; text-align: center; }
.evolution-card { padding: 10px 11px; border: 1px solid rgba(0,0,0,.07); border-radius: var(--td-radius-medium); background: rgba(255,255,255,.53); }
.evolution-item:last-child .evolution-card { border-color: rgba(7,192,95,.22); background: rgba(247,253,249,.68); }
.evolution-card__top { display: flex; align-items: flex-start; justify-content: space-between; gap: 8px; }
.evolution-card__top strong { font-size: 12px; line-height: 18px; }
.evolution-change-tag { flex: none; min-width: 34px; padding: 2px 6px; border-radius: 999px; color: var(--td-text-color-secondary); background: var(--td-bg-color-secondarycontainer); font-size: 10px; line-height: 16px; text-align: center; }
.evolution-change-tag.adjusted { color: var(--td-warning-color); background: var(--td-warning-color-light); }
.evolution-change-tag.confirmed { color: var(--td-brand-color); background: var(--td-brand-color-light); }
.evolution-summary { margin: 6px 0 9px; color: var(--td-text-color-secondary); font-size: 11px; line-height: 18px; }
.evolution-change, .evolution-result { display: grid; grid-template-columns: 48px minmax(0, 1fr); gap: 7px; padding: 8px 9px; font-size: 10px; line-height: 16px; }
.evolution-change { border-left: 2px solid var(--td-warning-color); background: color-mix(in srgb, var(--td-warning-color-light) 56%, rgba(255,255,255,.7)); }
.evolution-result { margin-top: 5px; background: var(--td-bg-color-secondarycontainer); }
.evolution-change span, .evolution-result span { color: var(--td-text-color-placeholder); }
.evolution-change strong, .evolution-result strong { overflow-wrap: anywhere; font-size: 10px; font-weight: 600; }
.evolution-context { display: flex; flex-wrap: wrap; gap: 5px 10px; margin-top: 8px; color: var(--td-text-color-placeholder); font-size: 10px; line-height: 15px; }
.evolution-video-link { display: inline-flex; align-items: center; gap: 4px; margin-top: 8px; border: 0; padding: 0; color: var(--td-brand-color); background: transparent; cursor: pointer; font-size: 10px; font-weight: 600; }
.evolution-video-link :deep(svg) { width: 13px; height: 13px; }
.todo-list, .knowledge-list { display: grid; gap: 8px; }
.todo-item { display: grid; grid-template-columns: 8px minmax(0, 1fr); gap: 9px; padding: 9px 10px; border: 1px solid rgba(0,0,0,.07); border-radius: var(--td-radius-medium); background: rgba(255,255,255,.55); }
.todo-status { width: 8px; height: 8px; margin-top: 5px; border-radius: 50%; background: var(--td-warning-color); }
.todo-status.progress { background: var(--td-brand-color); }.todo-status.done { background: var(--td-success-color); }
.todo-content { min-width: 0; }.todo-content > strong { display: block; font-size: 12px; line-height: 18px; }.todo-content > p { margin: 4px 0 0; color: var(--td-text-color-placeholder); font-size: 10px; line-height: 16px; }
.todo-evidence { margin: 9px 0 0; padding: 8px 9px; border: 0; border-left: 2px solid rgba(0,0,0,.2); background: var(--td-bg-color-secondarycontainer); }
.todo-evidence > span { display: block; color: var(--td-text-color-placeholder); font-size: 10px; line-height: 15px; }
.todo-evidence > p { margin: 4px 0 0; color: var(--td-text-color-secondary); font-size: 10px; line-height: 17px; }
.todo-evidence > button { display: inline-flex; align-items: center; gap: 4px; margin-top: 6px; padding: 0; border: 0; color: var(--td-brand-color); background: transparent; cursor: pointer; font-size: 10px; font-weight: 600; }
.todo-evidence > button :deep(svg) { width: 13px; height: 13px; }
.knowledge-item { display: grid; grid-template-columns: max-content minmax(0, 1fr) 15px; align-items: center; gap: 8px; padding: 9px 10px; border: 1px solid rgba(0,0,0,.07); border-radius: var(--td-radius-medium); color: inherit; background: rgba(255,255,255,.55); cursor: pointer; text-align: left; }
.knowledge-item:hover { border-color: rgba(7,192,95,.28); background: var(--td-brand-color-light); }.knowledge-item > span:nth-child(2) { display: grid; gap: 2px; min-width: 0; }.knowledge-item strong { overflow: hidden; font-size: 12px; text-overflow: ellipsis; white-space: nowrap; }.knowledge-item small { color: var(--td-text-color-placeholder); font-size: 10px; }.knowledge-item :deep(svg) { width: 14px; color: var(--td-text-color-placeholder); }
.knowledge-type { padding: 3px 6px; border-radius: 4px; font-size: 10px; }.knowledge-type.entity { color: #3f8f45; background: rgba(123,194,124,.2); }.knowledge-type.concept { color: #465f9c; background: rgba(101,129,192,.16); }.knowledge-type.case { color: #b15e3d; background: rgba(240,139,103,.18); }.knowledge-type.method { color: #6f7471; background: rgba(201,203,201,.46); }.knowledge-type.insight { color: #5f58a1; background: rgba(95,88,161,.13); }
.meeting-toast { position: fixed; z-index: 80; right: 24px; bottom: 24px; max-width: 360px; padding: 9px 13px; border-radius: var(--td-radius-medium); color: white; background: rgba(0,0,0,.78); box-shadow: var(--td-shadow-2); font-size: 12px; }
@media (max-width: 820px) { .meeting-content { grid-template-columns: 1fr; }.relation-board { min-height: 420px; }.meeting-detail { min-height: 520px; } }
@media (max-width: 720px) { .meeting-overview { grid-template-columns: repeat(2, minmax(0, 1fr)); }.relation-board { min-width: 640px; }.meeting-relations { overflow-x: auto; }.meeting-detail__tabs { gap: 15px; } }
</style>
