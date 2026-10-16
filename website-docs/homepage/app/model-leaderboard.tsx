"use client";
import { useEffect, useRef } from "react";
import { Header } from "./interactive";

function paintMetricBars(root: ParentNode) {
  root.querySelectorAll("tbody[data-bar-col]").forEach((tbody) => {
    const cells = Array.from(tbody.querySelectorAll<HTMLElement>("td.metric[data-v]"));
    if (!cells.length) return;
    const vals = cells.map((cell) => parseFloat(cell.dataset.v || "0"));
    const max = Math.max(...vals);
    const min = Math.min(...vals);
    const span = max - min || 1;
    cells.forEach((cell) => {
      const bar = cell.querySelector<HTMLElement>(".bar i");
      if (!bar) return;
      const pct = 12 + ((parseFloat(cell.dataset.v || "0") - min) / span) * 88;
      bar.style.width = `${pct.toFixed(1)}%`;
    });
  });
}

function scrollToSection(behavior: ScrollBehavior) {
  const id = window.location.hash.replace(/^#/, "");
  if (!id) return;
  const motion = window.matchMedia("(prefers-reduced-motion: reduce)").matches ? "auto" : behavior;
  document.getElementById(id)?.scrollIntoView({ behavior: motion, block: "start" });
}

export function ModelLeaderboard() {
  const rootRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const root = rootRef.current;
    if (!root) return;
    paintMetricBars(root);
    const cleanups: Array<() => void> = [];
    root.querySelectorAll<HTMLElement>("[data-tabs]").forEach((bar) => {
      const onClick = (event: Event) => {
        const btn = (event.target as HTMLElement).closest<HTMLElement>(".tab");
        if (!btn) return;
        bar.querySelectorAll(".tab").forEach((tab) => {
          tab.setAttribute("aria-selected", String(tab === btn));
        });
        const group = bar.dataset.tabs;
        root.querySelectorAll(`.panel[data-group="${group}"]`).forEach((panel) => {
          (panel as HTMLElement).hidden = panel.id !== btn.dataset.panel;
        });
      };
      bar.addEventListener("click", onClick);
      cleanups.push(() => bar.removeEventListener("click", onClick));
    });
    const onHash = () => scrollToSection("smooth");
    window.addEventListener("hashchange", onHash);
    scrollToSection("auto");
    return () => {
      cleanups.forEach((cleanup) => cleanup());
      window.removeEventListener("hashchange", onHash);
    };
  }, []);
  return (
    <div className="wk-models" ref={rootRef}>
      <a className="skip-link" href="#models-main">跳至正文</a>
      <Header />
      <div id="models-main">
<header className="hero">
  <div className="wrap hero-in">
    <h1>模型评测榜单</h1>
    <p className="lead">
      在相同数据以及配置参数下，横向对比 <strong>Chat</strong>、<strong>Embed</strong> 与 <strong>Rerank</strong> 的模型表现，并提供模型效果排行榜。
    </p>
  </div>
</header>

<main className="wrap">

  {/* ================= 1. Chat ================= */}
  <section id="llm">
    <div className="sec-head">
      <span className="sec-idx">01</span>
      <h2>LLM</h2>
      <span className="sec-sub">固定检索与精排，只替换 Chat 模型</span>
    </div>
    <div className="tabs" role="tablist" data-tabs="chat">
      <button className="tab" role="tab" aria-selected="true" data-panel="chat-agent">智能推理</button>
      <button className="tab" role="tab" aria-selected="false" data-panel="chat-knowledge">快速问答</button>
    </div>

    {/* 1.1 */}
    <div className="panel" id="chat-agent" data-group="chat">
      <div className="card">
        <div className="card-head">
          <h3>智能推理</h3>
          <span className="tag key">排序指标 综合分 6:3:1</span>
        </div>
        <div className="card-body">
          <div className="scroll">
            <table>
              <thead><tr>
                <th className="rank">#</th><th>Chat</th><th>Think Effort</th>
                <th className="num metric">综合分</th><th className="num">correctness 正确性</th><th className="num">completeness 完整度</th><th className="num">constraint 约束遵循</th>
                <th className="num">语料命中率</th><th className="num">平均语料数</th><th className="num">P50 迭代</th><th className="num">P50 token</th>
              </tr></thead>
              <tbody data-bar-col="2">
                <tr className="lead-row"><td><span className="rk top">1</span></td><td className="model">Grok 4.7</td><td className="dim">默认(high)</td><td className="metric" data-v="4.23"><span className="v">4.23</span><span className="bar"><i></i></span></td><td className="num">4.04</td><td className="num">4.41</td><td className="num">4.80</td><td className="num">95.19%</td><td className="num">3.69</td><td className="num">4</td><td className="num">344</td></tr>
                <tr className="lead-row"><td><span className="rk top2">2</span></td><td className="model">GPT-6 Astra</td><td className="dim">默认(medium)</td><td className="metric" data-v="4.04"><span className="v">4.04</span><span className="bar"><i></i></span></td><td className="num">3.95</td><td className="num">3.93</td><td className="num">4.88</td><td className="num">85.19%</td><td className="num">1.76</td><td className="num">4</td><td className="num">217</td></tr>
                <tr><td><span className="rk top2">3</span></td><td className="model">Claude Opus 5</td><td className="dim">默认(high)</td><td className="metric" data-v="3.89"><span className="v">3.89</span><span className="bar"><i></i></span></td><td className="num">3.57</td><td className="num">4.42</td><td className="num">4.16</td><td className="num">94.09%</td><td className="num">4.70</td><td className="num">6</td><td className="num">702</td></tr>
                <tr><td><span className="rk">4</span></td><td className="model">GLM-5.3</td><td className="dim">high</td><td className="metric" data-v="3.85"><span className="v">3.85</span><span className="bar"><i></i></span></td><td className="num">3.50</td><td className="num">4.26</td><td className="num">4.70</td><td className="num">90.92%</td><td className="num">2.64</td><td className="num">4</td><td className="num">475</td></tr>
                <tr><td><span className="rk">5</span></td><td className="model">DeepSeek-V4.1-Flash</td><td className="dim">默认(high)</td><td className="metric" data-v="3.78"><span className="v">3.78</span><span className="bar"><i></i></span></td><td className="num">3.45</td><td className="num">4.30</td><td className="num">4.15</td><td className="num">92.93%</td><td className="num">3.26</td><td className="num">4</td><td className="num">654</td></tr>
                <tr><td><span className="rk">6</span></td><td className="model">GLM-5.3-Flash</td><td className="dim">high</td><td className="metric" data-v="3.75"><span className="v">3.75</span><span className="bar"><i></i></span></td><td className="num">3.35</td><td className="num">4.26</td><td className="num">4.61</td><td className="num">87.81%</td><td className="num">2.47</td><td className="num">4</td><td className="num">507</td></tr>
                <tr><td><span className="rk">7</span></td><td className="model">Hunyuan-3</td><td className="dim">默认(high)</td><td className="metric" data-v="3.74"><span className="v">3.74</span><span className="bar"><i></i></span></td><td className="num">3.39</td><td className="num">4.19</td><td className="num">4.55</td><td className="num">88.30%</td><td className="num">2.74</td><td className="num">4</td><td className="num">574</td></tr>
                <tr><td><span className="rk">8</span></td><td className="model">Hunyuan-4-preview</td><td className="dim">默认(high)</td><td className="metric" data-v="3.72"><span className="v">3.72</span><span className="bar"><i></i></span></td><td className="num">3.30</td><td className="num">4.39</td><td className="num">4.22</td><td className="num">94.82%</td><td className="num">4.04</td><td className="num">8</td><td className="num">696</td></tr>
                <tr><td><span className="rk">9</span></td><td className="model">Qwen3.8-Flash-Next</td><td className="dim">默认(xhigh)</td><td className="metric" data-v="3.69"><span className="v">3.69</span><span className="bar"><i></i></span></td><td className="num">3.30</td><td className="num">4.29</td><td className="num">4.26</td><td className="num">92.81%</td><td className="num">3.69</td><td className="num">6</td><td className="num">601</td></tr>
                <tr><td><span className="rk">10</span></td><td className="model">Gemini-3.8-Flash</td><td className="dim">默认(medium)</td><td className="metric" data-v="3.62"><span className="v">3.62</span><span className="bar"><i></i></span></td><td className="num">3.23</td><td className="num">4.05</td><td className="num">4.72</td><td className="num">86.17%</td><td className="num">2.08</td><td className="num">3</td><td className="num">391</td></tr>
                <tr><td><span className="rk">11</span></td><td className="model">GPT-5.6 Sol</td><td className="dim">默认(medium)</td><td className="metric" data-v="3.59"><span className="v">3.59</span><span className="bar"><i></i></span></td><td className="num">3.26</td><td className="num">3.81</td><td className="num">4.89</td><td className="num">85.37%</td><td className="num">2.00</td><td className="num">4</td><td className="num">225</td></tr>
              </tbody>
            </table>
          </div>
          <div className="note">
            <strong>选型推荐</strong>
            <ul>
              <li><strong>质量优先选 Grok 4.7</strong> — 综合分第一，更忠于知识库内容。</li>
              <li><strong>开源模型优先 GLM-5.3</strong> — 综合分第四，迭代次数较少、输出 token 适中。</li>
            </ul>
          </div>
          <dl className="kv">
            <div><dt>correctness</dt><dd><b>陈述是否被证据支持</b></dd></div>
            <div><dt>completeness</dt><dd><b>关键子问题是否答全</b></dd></div>
            <div><dt>constraint</dt><dd><b>回答是否遵循明确及必要约束</b></dd></div>
            <div><dt>数据集</dt><dd><b>WeKnora 评测数据集</b></dd></div>
            <div><dt>判分模型</dt><dd><b>GPT-6 Astra</b></dd></div>
            <div><dt>分块</dt><dd><code>chunk_size=1000</code>、<code>chunk_overlap=200</code></dd></div>
            <div><dt>Embed</dt><dd><b>nvidia/Nemotron-3-Embed-8B-BF16</b></dd></div>
            <div><dt>Rerank</dt><dd><b>Qwen/Qwen3-Reranker-8B</b></dd></div>
            <div><dt>检索参数</dt><dd><code>embedding_top_k=50</code>、<code>vector_threshold=0.27</code>、<code>rerank_top_k=8</code>、<code>rerank_threshold=0.3</code>、<code>keyword_threshold=0.3</code></dd></div>
          </dl>
        </div>
      </div>
    </div>

    {/* 1.2 */}
    <div className="panel" id="chat-knowledge" data-group="chat" hidden>
      <div className="card">
        <div className="card-head">
          <h3>快速问答</h3>
          <span className="tag key">排序指标 综合分 6:3:1</span>
        </div>
        <div className="card-body">
          <div className="scroll">
            <table>
              <thead><tr>
                <th className="rank">#</th><th>Chat</th><th>Think</th>
                <th className="num metric">综合分</th><th className="num">correctness 正确性</th><th className="num">completeness 完整度</th><th className="num">constraint 约束遵循</th>
                <th className="num">P50 token</th>
              </tr></thead>
              <tbody data-bar-col="2">
                <tr className="lead-row"><td><span className="rk top">1</span></td><td className="model">GPT-6 Astra</td><td className="dim">默认(medium)</td><td className="metric" data-v="4.20"><span className="v">4.20</span><span className="bar"><i></i></span></td><td className="num">4.37</td><td className="num">3.75</td><td className="num">4.50</td><td className="num">186</td></tr>
                <tr className="lead-row"><td><span className="rk top2">2</span></td><td className="model">GPT-5.6 Sol</td><td className="dim">默认(medium)</td><td className="metric" data-v="3.99"><span className="v">3.99</span><span className="bar"><i></i></span></td><td className="num">4.03</td><td className="num">3.74</td><td className="num">4.55</td><td className="num">154</td></tr>
                <tr><td><span className="rk top2">3</span></td><td className="model">Gemini-3.8-Flash</td><td className="dim">默认(medium)</td><td className="metric" data-v="3.98"><span className="v">3.98</span><span className="bar"><i></i></span></td><td className="num">4.00</td><td className="num">3.78</td><td className="num">4.45</td><td className="num">250</td></tr>
                <tr><td><span className="rk">4</span></td><td className="model">Hunyuan-3</td><td className="dim">默认(high)</td><td className="metric" data-v="3.96"><span className="v">3.96</span><span className="bar"><i></i></span></td><td className="num">4.00</td><td className="num">3.76</td><td className="num">4.32</td><td className="num">357</td></tr>
                <tr><td><span className="rk">5</span></td><td className="model">DeepSeek-V4.1-Flash</td><td className="dim">默认(high)</td><td className="metric" data-v="3.93"><span className="v">3.93</span><span className="bar"><i></i></span></td><td className="num">3.94</td><td className="num">3.79</td><td className="num">4.26</td><td className="num">378</td></tr>
                <tr><td><span className="rk">6</span></td><td className="model">Hunyuan-4-preview</td><td className="dim">默认(high)</td><td className="metric" data-v="3.93"><span className="v">3.93</span><span className="bar"><i></i></span></td><td className="num">3.98</td><td className="num">3.80</td><td className="num">4.02</td><td className="num">589</td></tr>
                <tr><td><span className="rk">7</span></td><td className="model">Qwen3.8-Flash-Next</td><td className="dim">默认(xhigh)</td><td className="metric" data-v="3.88"><span className="v">3.88</span><span className="bar"><i></i></span></td><td className="num">3.86</td><td className="num">3.75</td><td className="num">4.35</td><td className="num">293</td></tr>
                <tr><td><span className="rk">8</span></td><td className="model">GLM-5.3-Flash</td><td className="dim">high</td><td className="metric" data-v="3.85"><span className="v">3.85</span><span className="bar"><i></i></span></td><td className="num">3.76</td><td className="num">3.84</td><td className="num">4.38</td><td className="num">375</td></tr>
                <tr><td><span className="rk">9</span></td><td className="model">GLM-5.3</td><td className="dim">high</td><td className="metric" data-v="3.83"><span className="v">3.83</span><span className="bar"><i></i></span></td><td className="num">3.74</td><td className="num">3.84</td><td className="num">4.38</td><td className="num">391</td></tr>
                <tr><td><span className="rk">10</span></td><td className="model">Grok 4.7</td><td className="dim">默认(high)</td><td className="metric" data-v="3.82"><span className="v">3.82</span><span className="bar"><i></i></span></td><td className="num">3.80</td><td className="num">3.71</td><td className="num">4.25</td><td className="num">270</td></tr>
                <tr><td><span className="rk">11</span></td><td className="model">Claude Opus 5</td><td className="dim">默认(high)</td><td className="metric" data-v="3.73"><span className="v">3.73</span><span className="bar"><i></i></span></td><td className="num">3.61</td><td className="num">3.88</td><td className="num">3.98</td><td className="num">604</td></tr>
              </tbody>
            </table>
          </div>
          <div className="note">
            <strong>选型推荐</strong>
            <ul>
              <li><strong>质量优先选 GPT-6 Astra</strong> — 综合分排名第一。</li>
              <li><strong>开源模型优先选 Hunyuan-3</strong> — 综合分可排在第二梯队。</li>
            </ul>
          </div>
          <dl className="kv">
            <div><dt>correctness</dt><dd><b>陈述是否被证据支持</b></dd></div>
            <div><dt>completeness</dt><dd><b>关键子问题是否答全</b></dd></div>
            <div><dt>constraint</dt><dd><b>回答是否遵循明确及必要约束</b></dd></div>
            <div><dt>数据集</dt><dd><b>WeKnora 评测数据集</b></dd></div>
            <div><dt>判分模型</dt><dd><b>GPT-6 Astra</b></dd></div>
            <div><dt>分块</dt><dd><code>chunk_size=1000</code>、<code>chunk_overlap=200</code></dd></div>
            <div><dt>Embed</dt><dd><b>nvidia/Nemotron-3-Embed-8B-BF16</b></dd></div>
            <div><dt>Rerank</dt><dd><b>Qwen/Qwen3-Reranker-8B</b></dd></div>
            <div><dt>检索参数</dt><dd><code>embedding_top_k=50</code>、<code>vector_threshold=0.27</code>、<code>rerank_top_k=8</code>、<code>rerank_threshold=0.3</code>、<code>keyword_threshold=0.3</code></dd></div>
          </dl>
        </div>
      </div>
    </div>

    <div className="card">
      <div className="card-head">
        <h3>榜单解读</h3>
      </div>
      <div className="card-body">
        <dl className="kv">
          <div><dt>智能推理领先，未必快速问答领先</dt><dd>快速问答侧重固定上下文下的答案生成，智能推理还考验检索规划、工具调用与证据整合。两种模式并非替代关系，需按场景使用，复杂问题使用智能推理，简单问题选择快速问答。</dd></div>
        </dl>
        <dl className="kv">
          <div><dt>存在自评偏差</dt><dd>GPT-6 Astra 自评会放大自身优势，但 Qwen3.8-27B 复评与其排名相关性仍达 0.80（快速问答）和 0.89（智能推理）。榜单可用于判断模型梯队、场景适配和明显差距；细微名次及自评领先幅度仍需多判官或人工盲审确认。</dd></div>
        </dl>
        <dl className="kv">
          <div><dt>思考强度的配置</dt><dd>优先选择官方api提供的默认配置，GLM 模型的默认思考强度(max)，易超出上下文限制16384故而降低一档</dd></div>
        </dl>
      </div>
    </div>
  </section>

  {/* ================= 2. Embed ================= */}
  <section id="embed">
    <div className="sec-head">
      <span className="sec-idx">02</span>
      <h2>Embed</h2>
      <span className="sec-sub">向量检索</span>
    </div>

    <div className="tabs" role="tablist" data-tabs="embed">
      <button className="tab" role="tab" aria-selected="true" data-panel="embed-rerank">2.1 召回率</button>
      <button className="tab" role="tab" aria-selected="false" data-panel="embed-only">2.2 召回排序</button>
    </div>

    {/* 2.1 */}
    <div className="panel" id="embed-rerank" data-group="embed">
      <div className="card">
        <div className="card-head">
          <h3>召回率</h3>
          <span className="tag key">排序指标 Recall@100</span>
          <span className="tag">适用：embed 召回后使用 rerank 进行精排，产品能提供 rerank 模型</span>
        </div>
        <div className="card-body">
          <div className="scroll" style={{ marginTop: 0 }}>
            <table>
              <thead><tr>
                <th className="rank">#</th><th>模型</th>
                <th className="num metric">Recall@100</th><th className="num">Recall@50</th><th className="num">Recall@10</th>
                <th className="num">推荐 retrieve k</th><th className="num">推荐 rerank/LLM k</th><th>推荐 vector_threshold</th>
              </tr></thead>
              <tbody data-bar-col="2">
                <tr className="lead-row"><td><span className="rk top">1</span></td><td className="model">nvidia/Nemotron-3-Embed-8B-BF16</td><td className="metric" data-v="0.92877"><span className="v">0.92877</span><span className="bar"><i></i></span></td><td className="num">0.91302</td><td className="num">0.85027</td><td className="num">50</td><td className="num">8</td><td className="dim">0.27</td></tr>
                <tr className="lead-row"><td><span className="rk top2">2</span></td><td className="model">tencent/KaLM-Embedding-Gemma3-12B-2511</td><td className="metric" data-v="0.92682"><span className="v">0.92682</span><span className="bar"><i></i></span></td><td className="num">0.90775</td><td className="num">0.82704</td><td className="num">50</td><td className="num">8</td><td className="dim">0.45</td></tr>
                <tr><td><span className="rk top2">3</span></td><td className="model">BAAI/bge-multilingual-gemma2</td><td className="metric" data-v="0.92134"><span className="v">0.92134</span><span className="bar"><i></i></span></td><td className="num">0.89952</td><td className="num">0.83392</td><td className="num">50</td><td className="num">8</td><td className="dim">0.25</td></tr>
                <tr><td><span className="rk">4</span></td><td className="model">nvidia/llama-embed-nemotron-8b</td><td className="metric" data-v="0.91899"><span className="v">0.91899</span><span className="bar"><i></i></span></td><td className="num">0.90133</td><td className="num">0.83465</td><td className="num">50</td><td className="num">8</td><td className="dim">0.28</td></tr>
                <tr><td><span className="rk">5</span></td><td className="model">Qwen/Qwen3-Embedding-8B</td><td className="metric" data-v="0.91704"><span className="v">0.91704</span><span className="bar"><i></i></span></td><td className="num">0.89429</td><td className="num">0.81379</td><td className="num">50–100</td><td className="num">8</td><td className="dim">0.44</td></tr>
                <tr><td><span className="rk">6</span></td><td className="model">infly/inf-retriever-v1</td><td className="metric" data-v="0.91663"><span className="v">0.91663</span><span className="bar"><i></i></span></td><td className="num">0.89984</td><td className="num">0.82624</td><td className="num">50</td><td className="num">8</td><td className="dim">0.52</td></tr>
                <tr><td><span className="rk">7</span></td><td className="model">Octen/Octen-Embedding-8B</td><td className="metric" data-v="0.91484"><span className="v">0.91484</span><span className="bar"><i></i></span></td><td className="num">0.89263</td><td className="num">0.81285</td><td className="num">50</td><td className="num">8</td><td className="dim">0.38</td></tr>
                <tr><td><span className="rk">8</span></td><td className="model">microsoft/harrier-oss-v1-27b</td><td className="metric" data-v="0.91351"><span className="v">0.91351</span><span className="bar"><i></i></span></td><td className="num">0.89578</td><td className="num">0.81882</td><td className="num">50</td><td className="num">8</td><td className="dim">不启用 / ≤0.50</td></tr>
                <tr><td><span className="rk">9</span></td><td className="model">tencent/WeMM-Embedding-9B</td><td className="metric" data-v="0.91289"><span className="v">0.91289</span><span className="bar"><i></i></span></td><td className="num">0.89092</td><td className="num">0.80677</td><td className="num">50</td><td className="num">8</td><td className="dim">0.41</td></tr>
                <tr><td><span className="rk">10</span></td><td className="model">tencent/WeMM-Embedding-4B</td><td className="metric" data-v="0.91271"><span className="v">0.91271</span><span className="bar"><i></i></span></td><td className="num">0.88344</td><td className="num">0.80074</td><td className="num">50</td><td className="num">8</td><td className="dim">0.41</td></tr>
                <tr><td><span className="rk">11</span></td><td className="model">infly/inf-retriever-v1-1.5b</td><td className="metric" data-v="0.91175"><span className="v">0.91175</span><span className="bar"><i></i></span></td><td className="num">0.88785</td><td className="num">0.81424</td><td className="num">50</td><td className="num">8</td><td className="dim">不启用 / ≤0.60</td></tr>
                <tr><td><span className="rk">12</span></td><td className="model">Alibaba-NLP/gte-Qwen2-7B-instruct</td><td className="metric" data-v="0.90712"><span className="v">0.90712</span><span className="bar"><i></i></span></td><td className="num">0.87931</td><td className="num">0.80102</td><td className="num">50</td><td className="num">8–10</td><td className="dim">0.44</td></tr>
                <tr><td><span className="rk">13</span></td><td className="model">Qwen/Qwen3-Embedding-4B</td><td className="metric" data-v="0.90702"><span className="v">0.90702</span><span className="bar"><i></i></span></td><td className="num">0.88952</td><td className="num">0.81706</td><td className="num">50</td><td className="num">8</td><td className="dim">0.45</td></tr>
                <tr><td><span className="rk">14</span></td><td className="model">google/embeddinggemma-300m</td><td className="metric" data-v="0.90589"><span className="v">0.90589</span><span className="bar"><i></i></span></td><td className="num">0.88321</td><td className="num">0.81084</td><td className="num">50</td><td className="num">8</td><td className="dim">0.37</td></tr>
                <tr><td><span className="rk">15</span></td><td className="model">codefuse-ai/F2LLM-v2-8B</td><td className="metric" data-v="0.90563"><span className="v">0.90563</span><span className="bar"><i></i></span></td><td className="num">0.88692</td><td className="num">0.79745</td><td className="num">50</td><td className="num">10</td><td className="dim">0.26</td></tr>
                <tr><td><span className="rk">16</span></td><td className="model">microsoft/harrier-oss-v1-0.6b</td><td className="metric" data-v="0.89011"><span className="v">0.89011</span><span className="bar"><i></i></span></td><td className="num">0.86811</td><td className="num">0.78097</td><td className="num">100</td><td className="num">10</td><td className="dim">0.49</td></tr>
                <tr><td><span className="rk">17</span></td><td className="model">BAAI/bge-m3</td><td className="metric" data-v="0.88338"><span className="v">0.88338</span><span className="bar"><i></i></span></td><td className="num">0.85892</td><td className="num">0.77859</td><td className="num">100</td><td className="num">10</td><td className="dim">0.47</td></tr>
                <tr><td><span className="rk">18</span></td><td className="model">Linq-AI-Research/Linq-Embed-Mistral</td><td className="metric" data-v="0.87430"><span className="v">0.87430</span><span className="bar"><i></i></span></td><td className="num">0.84657</td><td className="num">0.75117</td><td className="num">100</td><td className="num">10</td><td className="dim">0.45</td></tr>
                <tr><td><span className="rk">19</span></td><td className="model">jinaai/jina-embeddings-v5-text-small</td><td className="metric" data-v="0.86547"><span className="v">0.86547</span><span className="bar"><i></i></span></td><td className="num">0.84293</td><td className="num">0.76437</td><td className="num">100</td><td className="num">10</td><td className="dim">0.40</td></tr>
                <tr><td><span className="rk">20</span></td><td className="model">intfloat/multilingual-e5-large-instruct</td><td className="metric" data-v="0.83219"><span className="v">0.83219</span><span className="bar"><i></i></span></td><td className="num">0.80373</td><td className="num">0.71529</td><td className="num">100</td><td className="num">10</td><td className="dim">不启用</td></tr>
              </tbody>
            </table>
          </div>

          <div className="note">
            <strong>选型推荐</strong>
            <ul>
              <li><strong>首选 nvidia/Nemotron-3-Embed-8B-BF16</strong> — 召回覆盖最高，一套模型可同时用于直接检索和召回后精排。</li>
              <li><strong>备选 tencent/KaLM-Embedding-Gemma3-12B-2511</strong> — 覆盖率接近，retrieve 50 后即可交给精排。</li>
            </ul>
          </div>
        </div>
      </div>
    </div>

    {/* 2.2 */}
    <div className="panel" id="embed-only" data-group="embed" hidden>
      <div className="card">
        <div className="card-head">
          <h3>召回排序</h3>
          <span className="tag key">排序指标 NDCG@10</span>
          <span className="tag">适用：向量 top-k 直接送 LLM，产品无精排</span>
        </div>
        <div className="card-body">

          <div className="scroll" style={{ marginTop: 0 }}>
            <table>
              <thead><tr>
                <th className="rank">#</th><th>模型</th>
                <th className="num metric">NDCG@10</th><th className="num">Recall@10</th><th className="num">P@1</th>
                <th className="num">推荐 embedding_top_k</th><th>推荐 vector_threshold</th>
              </tr></thead>
              <tbody data-bar-col="2">
                <tr className="lead-row"><td><span className="rk top">1</span></td><td className="model">nvidia/Nemotron-3-Embed-8B-BF16</td><td className="metric" data-v="0.80114"><span className="v">0.80114</span><span className="bar"><i></i></span></td><td className="num">0.85027</td><td className="num">0.76872</td><td className="num">5–8</td><td className="dim">0.27</td></tr>
                <tr className="lead-row"><td><span className="rk top2">2</span></td><td className="model">nvidia/llama-embed-nemotron-8b</td><td className="metric" data-v="0.77419"><span className="v">0.77419</span><span className="bar"><i></i></span></td><td className="num">0.83465</td><td className="num">0.73281</td><td className="num">5–8</td><td className="dim">0.28</td></tr>
                <tr><td><span className="rk top2">3</span></td><td className="model">BAAI/bge-multilingual-gemma2</td><td className="metric" data-v="0.77355"><span className="v">0.77355</span><span className="bar"><i></i></span></td><td className="num">0.83392</td><td className="num">0.72855</td><td className="num">5–8</td><td className="dim">0.25</td></tr>
                <tr><td><span className="rk">4</span></td><td className="model">infly/inf-retriever-v1</td><td className="metric" data-v="0.76031"><span className="v">0.76031</span><span className="bar"><i></i></span></td><td className="num">0.82624</td><td className="num">0.71333</td><td className="num">8</td><td className="dim">0.52</td></tr>
                <tr><td><span className="rk">5</span></td><td className="model">microsoft/harrier-oss-v1-27b</td><td className="metric" data-v="0.75208"><span className="v">0.75208</span><span className="bar"><i></i></span></td><td className="num">0.81882</td><td className="num">0.70907</td><td className="num">8</td><td className="dim">不启用 / ≤0.50</td></tr>
                <tr><td><span className="rk">6</span></td><td className="model">tencent/KaLM-Embedding-Gemma3-12B-2511</td><td className="metric" data-v="0.75097"><span className="v">0.75097</span><span className="bar"><i></i></span></td><td className="num">0.82704</td><td className="num">0.69324</td><td className="num">8</td><td className="dim">0.45</td></tr>
                <tr><td><span className="rk">7</span></td><td className="model">Qwen/Qwen3-Embedding-4B</td><td className="metric" data-v="0.75008"><span className="v">0.75008</span><span className="bar"><i></i></span></td><td className="num">0.81706</td><td className="num">0.70481</td><td className="num">8</td><td className="dim">0.45</td></tr>
                <tr><td><span className="rk">8</span></td><td className="model">google/embeddinggemma-300m</td><td className="metric" data-v="0.75005"><span className="v">0.75005</span><span className="bar"><i></i></span></td><td className="num">0.81084</td><td className="num">0.70724</td><td className="num">8</td><td className="dim">0.37</td></tr>
                <tr><td><span className="rk">9</span></td><td className="model">Octen/Octen-Embedding-8B</td><td className="metric" data-v="0.74746"><span className="v">0.74746</span><span className="bar"><i></i></span></td><td className="num">0.81285</td><td className="num">0.70542</td><td className="num">8</td><td className="dim">0.38</td></tr>
                <tr><td><span className="rk">10</span></td><td className="model">infly/inf-retriever-v1-1.5b</td><td className="metric" data-v="0.74626"><span className="v">0.74626</span><span className="bar"><i></i></span></td><td className="num">0.81424</td><td className="num">0.69629</td><td className="num">8</td><td className="dim">不启用 / ≤0.60</td></tr>
                <tr><td><span className="rk">11</span></td><td className="model">Qwen/Qwen3-Embedding-8B</td><td className="metric" data-v="0.74496"><span className="v">0.74496</span><span className="bar"><i></i></span></td><td className="num">0.81379</td><td className="num">0.69324</td><td className="num">8–10</td><td className="dim">0.44</td></tr>
                <tr><td><span className="rk">12</span></td><td className="model">tencent/WeMM-Embedding-9B</td><td className="metric" data-v="0.73072"><span className="v">0.73072</span><span className="bar"><i></i></span></td><td className="num">0.80677</td><td className="num">0.67438</td><td className="num">10</td><td className="dim">0.41</td></tr>
                <tr><td><span className="rk">13</span></td><td className="model">tencent/WeMM-Embedding-4B</td><td className="metric" data-v="0.72771"><span className="v">0.72771</span><span className="bar"><i></i></span></td><td className="num">0.80074</td><td className="num">0.67498</td><td className="num">10</td><td className="dim">0.41</td></tr>
                <tr><td><span className="rk">14</span></td><td className="model">Alibaba-NLP/gte-Qwen2-7B-instruct</td><td className="metric" data-v="0.72140"><span className="v">0.72140</span><span className="bar"><i></i></span></td><td className="num">0.80102</td><td className="num">0.66951</td><td className="num">10</td><td className="dim">0.44</td></tr>
                <tr><td><span className="rk">15</span></td><td className="model">codefuse-ai/F2LLM-v2-8B</td><td className="metric" data-v="0.71855"><span className="v">0.71855</span><span className="bar"><i></i></span></td><td className="num">0.79745</td><td className="num">0.66525</td><td className="num">10</td><td className="dim">0.26</td></tr>
                <tr><td><span className="rk">16</span></td><td className="model">BAAI/bge-m3</td><td className="metric" data-v="0.70954"><span className="v">0.70954</span><span className="bar"><i></i></span></td><td className="num">0.77859</td><td className="num">0.65673</td><td className="num">10</td><td className="dim">0.47</td></tr>
                <tr><td><span className="rk">17</span></td><td className="model">microsoft/harrier-oss-v1-0.6b</td><td className="metric" data-v="0.70865"><span className="v">0.70865</span><span className="bar"><i></i></span></td><td className="num">0.78097</td><td className="num">0.65307</td><td className="num">10</td><td className="dim">0.49</td></tr>
                <tr><td><span className="rk">18</span></td><td className="model">jinaai/jina-embeddings-v5-text-small</td><td className="metric" data-v="0.69806"><span className="v">0.69806</span><span className="bar"><i></i></span></td><td className="num">0.76437</td><td className="num">0.64820</td><td className="num">10</td><td className="dim">0.40</td></tr>
                <tr><td><span className="rk">19</span></td><td className="model">Linq-AI-Research/Linq-Embed-Mistral</td><td className="metric" data-v="0.66628"><span className="v">0.66628</span><span className="bar"><i></i></span></td><td className="num">0.75117</td><td className="num">0.60316</td><td className="num">10</td><td className="dim">0.45</td></tr>
                <tr><td><span className="rk">20</span></td><td className="model">intfloat/multilingual-e5-large-instruct</td><td className="metric" data-v="0.63496"><span className="v">0.63496</span><span className="bar"><i></i></span></td><td className="num">0.71529</td><td className="num">0.57091</td><td className="num">10</td><td className="dim">不启用</td></tr>
              </tbody>
            </table>
          </div>

          <div className="note">
            <strong>选型推荐</strong>
            <ul>
              <li><strong>首选 nvidia/Nemotron-3-Embed-8B-BF16</strong> — 前排质量最高，适合取 top 5–8 直接送 LLM。</li>
              <li><strong>资源受限可选 google/embeddinggemma-300m</strong> — 仅 300M 参数，NDCG@10 与 Qwen3-4B 几乎持平。</li>
            </ul>
          </div>
        </div>
      </div>
    </div>


  </section>

  {/* ================= 3. Rerank ================= */}
  <section id="rerank">
    <div className="sec-head">
      <span className="sec-idx">03</span>
      <h2>Rerank</h2>
      <span className="sec-sub">固定向量召回的前 100 条候选，只比较精排模型</span>
    </div>
    <div className="card" style={{ marginTop: 22 }}>
      <div className="card-head">
        <h3>精排模型</h3>
        <span className="tag key">排序指标 NDCG@10</span>
      </div>
      <div className="card-body">
        <div className="scroll">
          <table>
            <thead><tr>
              <th className="rank">#</th><th>模型</th>
              <th className="num metric">NDCG@10</th><th className="num">P@1</th><th className="num">MRR@10</th><th className="num">Recall@10</th>
            </tr></thead>
            <tbody data-bar-col="2">
              <tr className="lead-row"><td><span className="rk top">1</span></td><td className="model">Qwen/Qwen3-Reranker-8B</td><td className="metric" data-v="0.83609"><span className="v">0.83609</span><span className="bar"><i></i></span></td><td className="num">0.81984</td><td className="num">0.87762</td><td className="num">0.87138</td></tr>
              <tr className="lead-row"><td><span className="rk top2">2</span></td><td className="model">jinaai/jina-reranker-v3.5</td><td className="metric" data-v="0.83271"><span className="v">0.83271</span><span className="bar"><i></i></span></td><td className="num">0.83019</td><td className="num">0.87968</td><td className="num">0.85638</td></tr>
              <tr><td><span className="rk top2">3</span></td><td className="model">nvidia/llama-nemotron-rerank-1b-v2</td><td className="metric" data-v="0.82449"><span className="v">0.82449</span><span className="bar"><i></i></span></td><td className="num">0.81010</td><td className="num">0.87286</td><td className="num">0.86411</td></tr>
              <tr><td><span className="rk">4</span></td><td className="model">BAAI/bge-reranker-v2-gemma</td><td className="metric" data-v="0.81764"><span className="v">0.81764</span><span className="bar"><i></i></span></td><td className="num">0.79732</td><td className="num">0.85972</td><td className="num">0.85650</td></tr>
              <tr><td><span className="rk">5</span></td><td className="model">Qwen/Qwen3-Reranker-0.6B</td><td className="metric" data-v="0.81003"><span className="v">0.81003</span><span className="bar"><i></i></span></td><td className="num">0.78211</td><td className="num">0.84985</td><td className="num">0.85363</td></tr>
              <tr><td><span className="rk">6</span></td><td className="model">BAAI/bge-reranker-v2-m3</td><td className="metric" data-v="0.80751"><span className="v">0.80751</span><span className="bar"><i></i></span></td><td className="num">0.79245</td><td className="num">0.84903</td><td className="num">0.84452</td></tr>
              <tr><td><span className="rk">7</span></td><td className="model">mixedbread-ai/mxbai-rerank-base-v2</td><td className="metric" data-v="0.80574"><span className="v">0.80574</span><span className="bar"><i></i></span></td><td className="num">0.78515</td><td className="num">0.84358</td><td className="num">0.84680</td></tr>
              <tr><td><span className="rk">8</span></td><td className="model">zeroentropy/zerank-2-reranker</td><td className="metric" data-v="0.79841"><span className="v">0.79841</span><span className="bar"><i></i></span></td><td className="num">0.77115</td><td className="num">0.83475</td><td className="num">0.84777</td></tr>
              <tr><td><span className="rk">9</span></td><td className="model">Alibaba-NLP/gte-multilingual-reranker-base</td><td className="metric" data-v="0.78978"><span className="v">0.78978</span><span className="bar"><i></i></span></td><td className="num">0.76019</td><td className="num">0.82483</td><td className="num">0.83848</td></tr>
              <tr><td><span className="rk">10</span></td><td className="model">jinaai/jina-reranker-v2-base-multilingual</td><td className="metric" data-v="0.78093"><span className="v">0.78093</span><span className="bar"><i></i></span></td><td className="num">0.75593</td><td className="num">0.82146</td><td className="num">0.82864</td></tr>
              <tr><td><span className="rk">11</span></td><td className="model">Alibaba-NLP/gte-reranker-modernbert-base</td><td className="metric" data-v="0.71176"><span className="v">0.71176</span><span className="bar"><i></i></span></td><td className="num">0.66586</td><td className="num">0.74086</td><td className="num">0.77675</td></tr>
              <tr><td><span className="rk">12</span></td><td className="model">Qwen/Qwen3-Reranker-4B</td><td className="metric" data-v="0.70408"><span className="v">0.70408</span><span className="bar"><i></i></span></td><td className="num">0.64760</td><td className="num">0.73794</td><td className="num">0.77447</td></tr>
            </tbody>
          </table>
        </div>
        <div className="note">
          <strong>选型推荐</strong>
          <ul>
            <li><strong>首选 Qwen/Qwen3-Reranker-8B</strong> — 综合排序质量最高，适合优先保证精排效果。</li>
            <li><strong>资源受限可选 jinaai/jina-reranker-v3.5</strong> — 仅 0.6B 参数，NDCG 接近首选，且首位命中表现更好。</li>
          </ul>
        </div>
        <dl className="kv">
          <div><dt>固定 Embed</dt><dd><b>nvidia/Nemotron-3-Embed-8B-BF16</b></dd></div>
          <div><dt>候选池</dt><dd>来自 Nemotron-3-Embed 前 100</dd></div>
          <div><dt>Recall@100</dt><dd><b>0.92877</b></dd></div>
        </dl>
      </div>
    </div>
  </section>

  {/* ================= 4. 数据集 ================= */}
  <section id="dataset">
    <div className="sec-head">
      <span className="sec-idx">04</span>
      <h2>数据集</h2>
      <span className="sec-sub">问题类型与语料构成</span>
    </div>
    <div className="card dataset-card" style={{ marginTop: 22 }}>
      <div className="card-body">
        <dl className="dataset-facts">
          <div className="dataset-count">
            <dt>数据规模</dt>
            <dd><b>1.6K</b> 条问答，<b>40K</b> 条语料</dd>
          </div>
          <div>
            <dt>问题分类</dt>
            <dd>
              <ul>
                <li>基础能力</li>
                <li>精确细节</li>
                <li>多跳推理</li>
                <li>反常识</li>
                <li>无答案</li>
                <li>干扰负例</li>
                <li>多版本答案</li>
                <li>结构化提取</li>
              </ul>
            </dd>
          </div>
          <div>
            <dt>语料大类</dt>
            <dd>
              <ul>
                <li>百科</li>
                <li>书籍</li>
                <li>新闻</li>
                <li>科普</li>
                <li>问答</li>
                <li>公众号文章</li>
              </ul>
              <span className="dataset-more">及其他来源</span>
            </dd>
          </div>
        </dl>
        <div className="note info dataset-note">
          <strong>数据与流程即将开源</strong>
          评测数据集、评估流程与更多模型的评测结果正在整理，将在近期陆续开源，届时在本页补充仓库与下载链接。
        </div>
      </div>
    </div>
  </section>

</main>

<footer>
  <div className="wrap foot-in">
    <span>WeKnora 模型评测</span>
    <span>数据截至 2026-10-08</span>
    <span>WeKnora 版本 v0.8.0</span>
  </div>
</footer>
      </div>
    </div>
  );
}
