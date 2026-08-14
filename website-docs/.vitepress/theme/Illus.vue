<script setup lang="ts">
/**
 * 首页线描插画。用内联 SVG 而不是位图：墨线取 currentColor、点缀取 --wk-gold，
 * 深浅色模式自动跟随，任意缩放不糊，整套加起来只有几 KB。
 */
defineProps<{ name: string }>()

const gold = 'var(--wk-gold)'

/**
 * 首屏全景图右半部分：九个客户端沿一段椭圆弧展开，连线从核心圆的边缘出发。
 * 角度、落点与曲线控制点都在这里算，避免在模板里堆一串手写坐标。
 */
const spokes = [-72, -54, -36, -18, 0, 18, 36, 54, 72].map((deg) => {
  const t = (deg * Math.PI) / 180
  const cx = 760 + 380 * Math.cos(t)
  const cy = 122 + 100 * Math.sin(t)
  const sx = 760 + 46 * Math.cos(t)
  const sy = 122 + 46 * Math.sin(t)
  const ex = cx - 15
  // 控制点按两端距离取，近的线不会拧成结，远的线自然接近直线
  const h = (ex - sx) * 0.45
  const f = (n: number) => n.toFixed(1)
  return {
    cx: Number(cx.toFixed(1)),
    cy: Number(cy.toFixed(1)),
    d: `M${f(sx)} ${f(sy)}C${f(sx + h)} ${f(sy)} ${f(ex - h)} ${f(cy)} ${f(ex)} ${f(cy)}`,
  }
})

/** 24×24 图标统一的线条参数 */
const s = {
  fill: 'none',
  stroke: 'currentColor',
  'stroke-width': 1.4,
  'stroke-linecap': 'round',
  'stroke-linejoin': 'round',
}
</script>

<template>
  <!-- ============ 首屏全景图 ============
       一张图讲完 WeKnora 与通用 RAG 示意图不一样的地方：
       左边多源接入（文档 / 网页 / 音频 / 图片）收束成一次统一解析，
       中间同一份内容并行写入四路索引（向量 / 关键词 / Wiki / 图谱），
       右边同一套知识库与 Agent 再扇形展开成九种客户端。

       版面规则：
       1. 全图是「收束 → 展开 → 收束 → 展开」的节奏，观者顺着疏密变化走，不需要标注；
       2. 连接一律走三次贝塞尔，控制点按两端距离取，不用直角折线；
       3. 九个客户端落在同一段椭圆弧上（角度在 script 里算），保证间距均匀；
       4. 笔触只有三档：主体 1.3、细节 1.1 且 opacity .45、连接线 opacity .3；
          金色只给四处焦点：解析框、核心圆、每路索引各一个记号。 -->
  <svg
    v-if="name === 'flow'"
    class="illus illus-flow"
    viewBox="0 0 1200 240"
    fill="none"
    aria-hidden="true"
  >
    <!-- ---------- 左：四类来源 ---------- -->
    <g transform="translate(66 45)" stroke="currentColor" stroke-linejoin="round">
      <path stroke-width="1.3" d="M-8-13h10l6 6v20H-8z" />
      <path stroke-width="1.3" d="M2-13v6h6" />
      <path stroke-width="1.1" opacity="0.45" d="M-4 1h8M-4 6h5" />
    </g>
    <g transform="translate(66 100)" stroke="currentColor">
      <circle stroke-width="1.3" cx="0" cy="0" r="11" />
      <ellipse stroke-width="1.3" cx="0" cy="0" rx="4.6" ry="11" />
      <path stroke-width="1.1" opacity="0.45" d="M-11 0h22" />
    </g>
    <g transform="translate(66 155)" stroke="currentColor" stroke-linecap="round">
      <path stroke-width="1.3" d="M-9-3.5v7M-4.5-6v12M4.5-5.5v11M9-3v6" />
      <path :stroke="gold" stroke-width="1.4" d="M0-8.5v17" />
    </g>
    <g transform="translate(66 210)" stroke="currentColor" stroke-linejoin="round">
      <rect stroke-width="1.3" x="-11" y="-9" width="22" height="18" rx="2.5" />
      <path stroke-width="1.1" opacity="0.5" d="M-7 6l5-6 3.5 4 3-3.5 4.5 5.5" />
      <circle :fill="gold" stroke="none" cx="-5" cy="-3.5" r="1.8" />
    </g>

    <!-- 来源汇入解析 -->
    <g stroke="currentColor" stroke-width="1.1" opacity="0.3">
      <path d="M82 45C150 45 186 102 248 104" />
      <path d="M82 100C150 100 190 114 248 116" />
      <path d="M82 155C150 155 190 130 248 128" />
      <path d="M82 210C150 210 186 140 248 140" />
    </g>

    <!-- ---------- 统一解析 ---------- -->
    <rect x="248" y="82" width="64" height="80" rx="12" fill="none" :stroke="gold" stroke-width="1.4" />
    <g stroke="currentColor" stroke-width="1.1" stroke-linecap="round" opacity="0.45">
      <path d="M264 104h32M264 120h32M264 136h20" />
    </g>

    <!-- 解析分流到四路索引 -->
    <g stroke="currentColor" stroke-width="1.1" opacity="0.3">
      <path d="M312 104C368 104 402 45 462 45" />
      <path d="M312 116C368 116 404 100 462 100" />
      <path d="M312 128C368 128 404 155 462 155" />
      <path d="M312 140C368 140 402 210 462 210" />
    </g>

    <!-- ---------- 四路索引 ---------- -->
    <!-- 向量：均匀点阵 -->
    <g :fill="gold" opacity="0.75">
      <circle cx="470" cy="39" r="2" />
      <circle cx="496" cy="39" r="2" />
      <circle cx="522" cy="39" r="2" />
      <circle cx="548" cy="39" r="2" />
      <circle cx="574" cy="39" r="2" />
      <circle cx="600" cy="39" r="2" />
      <circle cx="470" cy="51" r="2" />
      <circle cx="496" cy="51" r="2" />
      <circle cx="522" cy="51" r="2" />
      <circle cx="548" cy="51" r="2" />
      <circle cx="574" cy="51" r="2" />
      <circle cx="600" cy="51" r="2" />
    </g>

    <!-- 关键词：命中的词被标出来 -->
    <g stroke="currentColor" stroke-width="1.1" stroke-linecap="round" opacity="0.45">
      <path d="M470 94h130M534 106h66" />
    </g>
    <path d="M470 106h52" :stroke="gold" stroke-width="1.6" stroke-linecap="round" />

    <!-- Wiki：互链的两页 -->
    <g stroke="currentColor" stroke-width="1.3" stroke-linejoin="round">
      <rect x="470" y="141" width="44" height="22" rx="4" />
      <rect x="556" y="155" width="44" height="22" rx="4" />
    </g>
    <path d="M514 152C532 152 538 166 556 166" :stroke="gold" stroke-width="1.4" />

    <!-- 图谱：实体与关系 -->
    <g stroke="currentColor" stroke-width="1.1" opacity="0.4">
      <path d="M484 204C500 206 516 206 529 207" />
      <path d="M491 219C505 216 518 212 530 211" />
      <path d="M543 206C556 204 570 202 584 201" />
      <path d="M540 214C550 218 560 221 570 223" />
    </g>
    <g stroke="currentColor" stroke-width="1.3">
      <circle cx="478" cy="202" r="6" />
      <circle cx="486" cy="222" r="5.5" />
      <circle cx="590" cy="200" r="5.5" />
      <circle cx="575" cy="225" r="5" />
    </g>
    <circle cx="536" cy="208" r="7" fill="none" :stroke="gold" stroke-width="1.5" />

    <!-- 四路索引汇入核心 -->
    <g stroke="currentColor" stroke-width="1.1" opacity="0.3">
      <path d="M612 45C672 45 700 104 716 106" />
      <path d="M612 100C670 100 700 116 716 118" />
      <path d="M612 155C670 155 700 128 716 126" />
      <path d="M612 210C672 210 700 140 716 138" />
    </g>

    <!-- ---------- 核心：同一套知识库与 Agent ---------- -->
    <circle cx="760" cy="122" r="53" fill="none" stroke="currentColor" stroke-width="1" opacity="0.16" />
    <circle cx="760" cy="122" r="46" fill="none" :stroke="gold" stroke-width="1.5" />
    <g stroke="currentColor" stroke-width="1.3" stroke-linecap="round" opacity="0.75">
      <path d="M740 112h40M740 124h40M740 136h22" />
    </g>
    <circle cx="775" cy="136" r="3.4" :fill="gold" />

    <!-- 核心扇形展开到九种客户端 -->
    <g stroke="currentColor" stroke-width="1.1" opacity="0.3">
      <path v-for="(sp, i) in spokes" :key="i" :d="sp.d" />
    </g>

    <!-- ---------- 右：九种客户端 ---------- -->
    <!-- Web 控制台：窗口里是带出处标记的回答 -->
    <g :transform="`translate(${spokes[0].cx} ${spokes[0].cy})`">
      <rect x="-13" y="-10" width="26" height="20" rx="3.5" fill="none" stroke="currentColor" stroke-width="1.3" />
      <path d="M-13-4h26" stroke="currentColor" stroke-width="1.1" opacity="0.45" />
      <circle cx="-9" cy="-7" r="1.4" :fill="gold" />
      <path d="M-9 1h13" stroke="currentColor" stroke-width="1.1" stroke-linecap="round" opacity="0.4" />
      <rect x="-9" y="4" width="7" height="4" rx="1.5" fill="none" :stroke="gold" stroke-width="1.2" />
    </g>

    <!-- 桌面客户端 -->
    <g :transform="`translate(${spokes[1].cx} ${spokes[1].cy})`" fill="none">
      <rect x="-13" y="-11" width="26" height="17" rx="3" stroke="currentColor" stroke-width="1.3" />
      <path stroke="currentColor" stroke-width="1.3" stroke-linecap="round" d="M-5 11h10M0 6v5" />
      <path :stroke="gold" stroke-width="1.2" stroke-linecap="round" d="M-8-5h11" />
    </g>

    <!-- Chrome 插件：网页右侧的问答边栏 -->
    <g :transform="`translate(${spokes[2].cx} ${spokes[2].cy})`" fill="none">
      <rect x="-13" y="-10" width="26" height="20" rx="3.5" stroke="currentColor" stroke-width="1.3" />
      <path d="M-9-4h9M-9 1h6" stroke="currentColor" stroke-width="1.1" stroke-linecap="round" opacity="0.4" />
      <path :stroke="gold" stroke-width="1.3" d="M4-10v20" />
      <path :stroke="gold" stroke-width="1.1" stroke-linecap="round" opacity="0.75" d="M7-3h4M7 2h4" />
    </g>

    <!-- 网页嵌入挂件：站点角上的悬浮问答 -->
    <g :transform="`translate(${spokes[3].cx} ${spokes[3].cy})`" fill="none">
      <rect x="-13" y="-11" width="26" height="22" rx="3.5" stroke="currentColor" stroke-width="1.3" />
      <path d="M-9-6h10M-9-1h7" stroke="currentColor" stroke-width="1.1" stroke-linecap="round" opacity="0.4" />
      <path :stroke="gold" stroke-width="1.3" stroke-linejoin="round" d="M0 2h11v7H5l-3 2.6V9H0z" />
    </g>

    <!-- IM 机器人 -->
    <g :transform="`translate(${spokes[4].cx} ${spokes[4].cy})`" fill="none">
      <rect x="-11" y="-6" width="22" height="17" rx="5" stroke="currentColor" stroke-width="1.3" />
      <path d="M0-10.5v4.5" stroke="currentColor" stroke-width="1.3" stroke-linecap="round" />
      <circle cx="0" cy="-12" r="1.6" stroke="currentColor" stroke-width="1.2" />
      <g :fill="gold">
        <circle cx="-4" cy="2.5" r="1.7" />
        <circle cx="4" cy="2.5" r="1.7" />
      </g>
    </g>

    <!-- 微信小程序 -->
    <g :transform="`translate(${spokes[5].cx} ${spokes[5].cy})`" fill="none">
      <rect x="-7" y="-12" width="14" height="24" rx="3.5" stroke="currentColor" stroke-width="1.3" />
      <rect x="-4" y="-6" width="8" height="8" rx="2" :stroke="gold" stroke-width="1.3" />
      <path d="M-2.5 8.5h5" stroke="currentColor" stroke-width="1.1" stroke-linecap="round" opacity="0.45" />
    </g>

    <!-- 命令行 -->
    <g :transform="`translate(${spokes[6].cx} ${spokes[6].cy})`" fill="none">
      <rect x="-13" y="-10" width="26" height="20" rx="3.5" stroke="currentColor" stroke-width="1.3" />
      <path :stroke="gold" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round" d="M-7-3.5L-3.5 0-7 3.5" />
      <path d="M0 4h7" stroke="currentColor" stroke-width="1.1" stroke-linecap="round" opacity="0.45" />
    </g>

    <!-- REST API 与 SDK -->
    <g :transform="`translate(${spokes[7].cx} ${spokes[7].cy})`" fill="none">
      <g stroke="currentColor" stroke-width="1.3" stroke-linecap="round">
        <path d="M-4-10c-2.8 0-3.2 1.4-3.2 3.4v2.6c0 2-1 3-2.6 3 1.6 0 2.6 1 2.6 3v2.6c0 2 .4 3.4 3.2 3.4" />
        <path d="M4-10c2.8 0 3.2 1.4 3.2 3.4v2.6c0 2 1 3 2.6 3-1.6 0-2.6 1-2.6 3v2.6c0 2-.4 3.4-3.2 3.4" />
      </g>
      <circle cx="0" cy="1" r="1.8" :fill="gold" />
    </g>

    <!-- MCP：双向 -->
    <g :transform="`translate(${spokes[8].cx} ${spokes[8].cy})`" fill="none">
      <g stroke="currentColor" stroke-width="1.3">
        <rect x="-13" y="-7" width="9" height="14" rx="2.5" />
        <rect x="4" y="-7" width="9" height="14" rx="2.5" />
      </g>
      <path :stroke="gold" stroke-width="1.3" stroke-linecap="round" stroke-linejoin="round" d="M-2.5-3h5.5M1-5l2 2-2 2" />
      <path stroke="currentColor" stroke-width="1.2" stroke-linecap="round" stroke-linejoin="round" opacity="0.6" d="M2.5 3h-5.5M-1 1l-2 2 2 2" />
    </g>
  </svg>

  <!-- ==================== 处理流程 四枚 ==================== -->

  <!-- 读懂文档：版式还原 -->
  <svg v-else-if="name === 'read'" class="illus" viewBox="0 0 24 24" aria-hidden="true">
    <path v-bind="s" d="M5 3h9l5 5v13H5z" />
    <path v-bind="s" d="M14 3v5h5" />
    <path v-bind="s" opacity="0.5" d="M8 12h8M8 15.5h8" />
    <path :stroke="gold" stroke-width="1.4" stroke-linecap="round" d="M8 19h5" />
  </svg>

  <!-- 切好知识：父块套子块 -->
  <svg v-else-if="name === 'chunk'" class="illus" viewBox="0 0 24 24" aria-hidden="true">
    <rect v-bind="s" x="3" y="3.5" width="18" height="17" rx="2" />
    <path v-bind="s" opacity="0.5" d="M3 9h18M3 15h18" />
    <rect x="5.5" y="10" width="9" height="4" rx="1" fill="none" :stroke="gold" stroke-width="1.4" />
  </svg>

  <!-- 找准证据：两路召回汇合成一份排好序的结果 -->
  <svg v-else-if="name === 'retrieve'" class="illus" viewBox="0 0 24 24" aria-hidden="true">
    <path v-bind="s" d="M2.5 5.5c5.5 0 3.5 6.5 8 6.5" />
    <path v-bind="s" d="M2.5 18.5c5.5 0 3.5-6.5 8-6.5" />
    <path :stroke="gold" stroke-width="1.6" stroke-linecap="round" d="M13.5 8.5h8" />
    <path v-bind="s" opacity="0.55" d="M13.5 12.5h6M13.5 16.5h4" />
  </svg>

  <!-- 可核对的回答：气泡 + 出处角标 -->
  <svg v-else-if="name === 'answer'" class="illus" viewBox="0 0 24 24" aria-hidden="true">
    <path v-bind="s" d="M4 4h16v12H9l-5 4z" />
    <path v-bind="s" opacity="0.5" d="M7.5 8.5h9M7.5 12h5" />
    <circle cx="17" cy="12" r="2" fill="none" :stroke="gold" stroke-width="1.4" />
  </svg>

  <!-- ==================== 核心能力 九枚 ==================== -->

  <!-- Wiki：互链的页面 -->
  <svg v-else-if="name === 'wiki'" class="illus" viewBox="0 0 24 24" aria-hidden="true">
    <rect v-bind="s" x="2.5" y="3" width="8" height="10" rx="1.5" />
    <rect v-bind="s" x="13.5" y="11" width="8" height="10" rx="1.5" />
    <path :stroke="gold" stroke-width="1.4" stroke-linecap="round" fill="none" d="M10.5 8h5a2 2 0 012 2v1" />
  </svg>

  <!-- 分块编辑与版本：历史堆叠 -->
  <svg v-else-if="name === 'version'" class="illus" viewBox="0 0 24 24" aria-hidden="true">
    <rect v-bind="s" opacity="0.45" x="3" y="3" width="14" height="8" rx="1.5" />
    <rect v-bind="s" x="7" y="9.5" width="14" height="8" rx="1.5" />
    <path :stroke="gold" stroke-width="1.4" stroke-linecap="round" d="M6 21h12" />
  </svg>

  <!-- 多渠道：中心向外分发 -->
  <svg v-else-if="name === 'channels'" class="illus" viewBox="0 0 24 24" aria-hidden="true">
    <circle cx="12" cy="12" r="3" fill="none" :stroke="gold" stroke-width="1.4" />
    <path v-bind="s" opacity="0.7" d="M12 9V4.8M12 15v4.2M9 12H4.8M15 12h4.2" />
    <g v-bind="s" opacity="0.55">
      <circle cx="12" cy="3.4" r="1.4" />
      <circle cx="12" cy="20.6" r="1.4" />
      <circle cx="3.4" cy="12" r="1.4" />
      <circle cx="20.6" cy="12" r="1.4" />
    </g>
  </svg>

  <!-- MCP：双向 -->
  <svg v-else-if="name === 'mcp'" class="illus" viewBox="0 0 24 24" aria-hidden="true">
    <rect v-bind="s" x="2.5" y="6" width="7" height="12" rx="1.5" />
    <rect v-bind="s" x="14.5" y="6" width="7" height="12" rx="1.5" />
    <path
      :stroke="gold"
      stroke-width="1.4"
      stroke-linecap="round"
      stroke-linejoin="round"
      fill="none"
      d="M10 9.5h4.5M13 8l1.6 1.5"
    />
    <path v-bind="s" d="M14 14.5H9.5M11.1 13l-1.6 1.5" />
  </svg>

  <!-- 多空间隔离与审计：盾 -->
  <svg v-else-if="name === 'govern'" class="illus" viewBox="0 0 24 24" aria-hidden="true">
    <path v-bind="s" d="M12 2.8l7.5 3v6.4c0 4-3.2 7.4-7.5 9-4.3-1.6-7.5-5-7.5-9V5.8z" />
    <path
      :stroke="gold"
      stroke-width="1.5"
      stroke-linecap="round"
      stroke-linejoin="round"
      fill="none"
      d="M8.8 12.2l2.3 2.3 4.1-4.4"
    />
  </svg>

  <!-- 可插拔：底板上的可替换模块 -->
  <svg v-else-if="name === 'pluggable'" class="illus" viewBox="0 0 24 24" aria-hidden="true">
    <rect v-bind="s" x="2.5" y="4" width="19" height="16" rx="2" />
    <path v-bind="s" opacity="0.5" d="M2.5 9.5h5M2.5 14.5h5" />
    <rect x="10" y="8" width="8" height="8" rx="1.5" fill="none" :stroke="gold" stroke-width="1.4" />
  </svg>

  <!-- 知识图谱：实体与关系 -->
  <svg v-else-if="name === 'graph'" class="illus" viewBox="0 0 24 24" aria-hidden="true">
    <path v-bind="s" opacity="0.5" d="M7.2 5.9l9.2 -1.4M6.4 8.7l9.6 8.2M9.9 19.4l5.4 -0.9" />
    <circle v-bind="s" cx="5" cy="6.5" r="2.3" />
    <circle v-bind="s" cx="7.6" cy="19.6" r="2.3" />
    <circle v-bind="s" cx="17.6" cy="18.2" r="2.3" />
    <circle cx="18.6" cy="4.4" r="2.4" fill="none" :stroke="gold" stroke-width="1.5" />
  </svg>

  <!-- 数据源：按计划同步 -->
  <svg v-else-if="name === 'sync'" class="illus" viewBox="0 0 24 24" aria-hidden="true">
    <path v-bind="s" d="M20 12a8 8 0 10-2.6 5.9" />
    <path
      :stroke="gold"
      stroke-width="1.5"
      stroke-linecap="round"
      stroke-linejoin="round"
      fill="none"
      d="M20.6 7.6V12h-4.4"
    />
    <path v-bind="s" opacity="0.5" d="M12 7.8V12l2.8 1.8" />
  </svg>

  <!-- ==================== 部署形态 三枚 ==================== -->

  <!-- Docker Compose：一叠一起起来的服务 -->
  <svg v-else-if="name === 'compose'" class="illus" viewBox="0 0 24 24" aria-hidden="true">
    <rect x="3" y="3.5" width="18" height="5" rx="1.4" fill="none" :stroke="gold" stroke-width="1.4" />
    <g v-bind="s">
      <rect x="3" y="10.5" width="18" height="5" rx="1.4" />
      <rect x="3" y="17.5" width="18" height="5" rx="1.4" />
    </g>
    <g :fill="gold" opacity="0.85">
      <circle cx="6.6" cy="13" r="0.9" />
      <circle cx="6.6" cy="20" r="0.9" />
    </g>
  </svg>

  <!-- Helm / Kubernetes：舵轮 -->
  <svg v-else-if="name === 'helm'" class="illus" viewBox="0 0 24 24" aria-hidden="true">
    <path v-bind="s" d="M12 2.6l8.2 4v7.6L12 21.4 3.8 14.2V6.6z" />
    <circle cx="12" cy="12" r="2.4" fill="none" :stroke="gold" stroke-width="1.4" />
    <path v-bind="s" opacity="0.55" d="M12 6.4v3.2M9.9 13.4l-2.6 1.9M14.1 13.4l2.6 1.9" />
  </svg>

  <!-- Lite：单机一台 -->
  <svg v-else-if="name === 'lite'" class="illus" viewBox="0 0 24 24" aria-hidden="true">
    <rect v-bind="s" x="4" y="4" width="16" height="11" rx="1.8" />
    <path v-bind="s" opacity="0.5" d="M2.5 18.5h19" />
    <rect x="9" y="7.5" width="6" height="4" rx="1" fill="none" :stroke="gold" stroke-width="1.4" />
  </svg>

  <!-- ==================== 文档地图 六枚 ==================== -->

  <!-- 快速开始：起播 -->
  <svg v-else-if="name === 'start'" class="illus" viewBox="0 0 24 24" aria-hidden="true">
    <circle v-bind="s" cx="12" cy="12" r="9" />
    <path
      :stroke="gold"
      stroke-width="1.5"
      stroke-linejoin="round"
      stroke-linecap="round"
      fill="none"
      d="M10 8.4l6 3.6-6 3.6z"
    />
  </svg>

  <!-- 架构：自上而下的层级 -->
  <svg v-else-if="name === 'arch'" class="illus" viewBox="0 0 24 24" aria-hidden="true">
    <rect x="8.5" y="2.5" width="7" height="5" rx="1.2" fill="none" :stroke="gold" stroke-width="1.4" />
    <rect v-bind="s" x="2.5" y="16" width="7" height="5" rx="1.2" />
    <rect v-bind="s" x="14.5" y="16" width="7" height="5" rx="1.2" />
    <path v-bind="s" opacity="0.6" d="M12 7.5v4M6 16v-4.5h12V16" />
  </svg>

  <!-- 功能模块：栅格 -->
  <svg v-else-if="name === 'modules'" class="illus" viewBox="0 0 24 24" aria-hidden="true">
    <g v-bind="s">
      <rect x="3" y="3" width="7" height="7" rx="1.4" />
      <rect x="3" y="14" width="7" height="7" rx="1.4" />
      <rect x="14" y="14" width="7" height="7" rx="1.4" />
    </g>
    <rect x="14" y="3" width="7" height="7" rx="1.4" fill="none" :stroke="gold" stroke-width="1.4" />
  </svg>

  <!-- 客户端：多端 -->
  <svg v-else-if="name === 'clients'" class="illus" viewBox="0 0 24 24" aria-hidden="true">
    <path v-bind="s" d="M14.5 17H3.5a1 1 0 01-1-1V5a1 1 0 011-1h15a1 1 0 011 1v2.5" />
    <path v-bind="s" opacity="0.5" d="M7.5 20h5M10 17v3" />
    <rect x="15" y="10" width="6.5" height="10" rx="1.5" fill="none" :stroke="gold" stroke-width="1.4" />
  </svg>

  <!-- 开发指南：源码尖括号 -->
  <svg v-else-if="name === 'dev'" class="illus" viewBox="0 0 24 24" aria-hidden="true">
    <path v-bind="s" d="M8 7.5L3.5 12 8 16.5M16 7.5l4.5 4.5-4.5 4.5" />
    <path :stroke="gold" stroke-width="1.5" stroke-linecap="round" d="M13.6 5.5l-3.2 13" />
  </svg>

  <!-- ==================== 客户端入口 九枚 ==================== -->

  <!-- Web 控制台：浏览器窗口 -->
  <svg v-else-if="name === 'console'" class="illus" viewBox="0 0 24 24" aria-hidden="true">
    <rect v-bind="s" x="2.5" y="4" width="19" height="16" rx="2" />
    <path v-bind="s" d="M2.5 8.5h19" />
    <circle :fill="gold" cx="5.6" cy="6.2" r="0.9" />
    <path v-bind="s" opacity="0.45" d="M6 12.5h8M6 16h5" />
  </svg>

  <!-- Chrome 插件：网页右侧的问答边栏 -->
  <svg v-else-if="name === 'extension'" class="illus" viewBox="0 0 24 24" aria-hidden="true">
    <rect v-bind="s" x="2.5" y="4" width="19" height="16" rx="2" />
    <path v-bind="s" opacity="0.45" d="M5.5 8.5h6M5.5 12h4" />
    <path :stroke="gold" stroke-width="1.4" d="M14.5 4v16" />
    <path :stroke="gold" stroke-width="1.4" stroke-linecap="round" opacity="0.75" d="M17 9h2M17 12.5h2" />
  </svg>

  <!-- 网页嵌入挂件：站点角上的悬浮问答 -->
  <svg v-else-if="name === 'embed'" class="illus" viewBox="0 0 24 24" aria-hidden="true">
    <rect v-bind="s" x="2.5" y="3.5" width="19" height="17" rx="2" />
    <path v-bind="s" opacity="0.45" d="M6 8h7M6 11.5h5" />
    <path
      :stroke="gold"
      stroke-width="1.4"
      stroke-linejoin="round"
      fill="none"
      d="M12.5 13.5h6.5v3.5h-4l-2.5 1.9z"
    />
  </svg>

  <!-- 桌面客户端：显示器 -->
  <svg v-else-if="name === 'desktop'" class="illus" viewBox="0 0 24 24" aria-hidden="true">
    <rect v-bind="s" x="2.5" y="4" width="19" height="12.5" rx="2" />
    <path v-bind="s" d="M9.5 20h5M12 16.5V20" />
    <path :stroke="gold" stroke-width="1.4" stroke-linecap="round" d="M6.5 8.5h7" />
  </svg>

  <!-- IM 机器人 -->
  <svg v-else-if="name === 'bot'" class="illus" viewBox="0 0 24 24" aria-hidden="true">
    <rect v-bind="s" x="3.5" y="7.5" width="17" height="12" rx="3" />
    <path v-bind="s" d="M12 4.2v3.3" />
    <circle v-bind="s" cx="12" cy="3.2" r="1.2" />
    <g :fill="gold">
      <circle cx="9" cy="13.2" r="1.3" />
      <circle cx="15" cy="13.2" r="1.3" />
    </g>
  </svg>

  <!-- 微信小程序：手机 -->
  <svg v-else-if="name === 'mobile'" class="illus" viewBox="0 0 24 24" aria-hidden="true">
    <rect v-bind="s" x="6" y="2.5" width="12" height="19" rx="2.5" />
    <path v-bind="s" opacity="0.5" d="M10.6 19h2.8" />
    <rect x="9" y="7" width="6" height="6" rx="1.4" fill="none" :stroke="gold" stroke-width="1.4" />
  </svg>

  <!-- 命令行 -->
  <svg v-else-if="name === 'cli'" class="illus" viewBox="0 0 24 24" aria-hidden="true">
    <rect v-bind="s" x="2.5" y="4" width="19" height="16" rx="2" />
    <path
      :stroke="gold"
      stroke-width="1.5"
      stroke-linecap="round"
      stroke-linejoin="round"
      fill="none"
      d="M6.5 9.5L9.5 12l-3 2.5"
    />
    <path v-bind="s" opacity="0.5" d="M12 15h5.5" />
  </svg>

  <!-- REST API 与 SDK：花括号 -->
  <svg v-else-if="name === 'api'" class="illus" viewBox="0 0 24 24" aria-hidden="true">
    <path v-bind="s" d="M9 3.5c-2.2 0-2.6 1.2-2.6 3v2.3c0 1.8-.8 2.6-2.4 3.2 1.6.6 2.4 1.4 2.4 3.2v2.3c0 1.8.4 3 2.6 3" />
    <path v-bind="s" d="M15 3.5c2.2 0 2.6 1.2 2.6 3v2.3c0 1.8.8 2.6 2.4 3.2-1.6.6-2.4 1.4-2.4 3.2v2.3c0 1.8-.4 3-2.6 3" />
    <circle :fill="gold" cx="12" cy="12" r="1.5" />
  </svg>

  <!-- FAQ：问答对 -->
  <svg v-else-if="name === 'faq'" class="illus" viewBox="0 0 24 24" aria-hidden="true">
    <path v-bind="s" d="M3 3.5h13v9H8l-5 4z" />
    <path v-bind="s" opacity="0.5" d="M6 7h7M6 10h4" />
    <path
      :stroke="gold"
      stroke-width="1.4"
      stroke-linecap="round"
      stroke-linejoin="round"
      fill="none"
      d="M21 15.5h-8.5v4.5H18l3 2.2z"
    />
  </svg>
</template>

<style scoped>
.illus {
  display: block;
  width: 100%;
  height: 100%;
  overflow: visible;
}
</style>
