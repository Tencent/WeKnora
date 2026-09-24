const state = {
  datasets: [],
  project: null,
  dirty: false,
  editingPassageID: null,
  editingQuestionID: null,
  questionPassageSelection: new Set(),
  evaluationRuns: [],
  connectionProfiles: [],
  compatibilityReport: null,
  externalResultPreview: null,
  evaluationReport: null,
  feedbackQuestionIDs: new Set(),
  generationConnections: [],
  generationJobs: [],
  generationJob: null,
  generationPassageSelection: new Set(),
  generationPollTimer: null,
  sourceImport: {
    knowledgeBases: [], documents: [], chunks: [], selectedChunkIDs: new Set(),
    chunkPage: 1, chunkPageSize: 20, chunkTotal: 0, preview: null, importedPassageIDs: [],
  },
  settings: { schema_version: 1, default_generation_profile_id: "", default_evaluation_profile_id: "" },
};

const byId = (id) => document.getElementById(id);
const datasetList = byId("dataset-list");
const emptyState = byId("empty-state");
const editor = byId("editor");
const saveState = byId("save-state");

function initializeConfigurationLayout() {
  byId("generation-configuration-host").append(byId("generation-configuration-panel"));
  const evaluationPanel = byId("evaluation-configuration-panel");
  const evaluationFields = byId("remote-evaluation-fields");
  const configurationForm = evaluationPanel.querySelector(".stack-form");
  const toolbar = configurationForm.querySelector(".toolbar");
  configurationForm.insertBefore(evaluationFields, toolbar);
  byId("evaluation-configuration-host").append(evaluationPanel);
}

function showWorkspace(name, section = "") {
  const configuration = name === "configuration";
  byId("dataset-workspace").hidden = configuration;
  byId("configuration-workspace").hidden = !configuration;
  byId("show-datasets-button").classList.toggle("active", !configuration);
  byId("show-config-button").classList.toggle("active", configuration);
  saveState.hidden = configuration;
  if (configuration && section) {
    requestAnimationFrame(() => byId(`${section}-configuration-section`)?.scrollIntoView({ behavior: "smooth", block: "start" }));
  }
}

async function api(path, options = {}) {
  const response = await fetch(path, options);
  const contentType = response.headers.get("content-type") || "";
  const body = contentType.includes("application/json") ? await response.json() : null;
  if (!response.ok) {
    const error = new Error(body?.error?.message || `请求失败 (${response.status})`);
    error.payload = body;
    throw error;
  }
  return body;
}

function showToast(message, isError = false) {
  const toast = byId("toast");
  toast.textContent = message;
  toast.classList.toggle("error", isError);
  toast.hidden = false;
  clearTimeout(showToast.timer);
  showToast.timer = setTimeout(() => { toast.hidden = true; }, 3200);
}

function setDirty(dirty = true) {
  state.dirty = dirty;
  if (!state.project) {
    saveState.textContent = "未打开数据集";
    saveState.classList.remove("dirty");
    return;
  }
  saveState.textContent = dirty ? "有未保存修改" : "已保存";
  saveState.classList.toggle("dirty", dirty);
}

function parseTags(value) {
  return [...new Set(value.split(",").map((item) => item.trim()).filter(Boolean))];
}

function tagsText(tags) {
  return (tags || []).join(", ");
}

function parseLineList(value) {
  return [...new Set(value.split(/[\n|]/).map((item) => item.trim()).filter(Boolean))];
}

function lineListText(items) {
  return (items || []).join("\n");
}

function parseKeyValueLines(value) {
  const result = {};
  for (const part of value.split(/[\n|]/).map((item) => item.trim()).filter(Boolean)) {
    const separator = part.indexOf("=");
    const key = separator < 0 ? "" : part.slice(0, separator).trim();
    const item = separator < 0 ? "" : part.slice(separator + 1).trim();
    if (!key || !item) throw new Error(`检索过滤条件“${part}”必须使用 key=value 格式`);
    if (Object.hasOwn(result, key)) throw new Error(`检索过滤条件键“${key}”重复`);
    result[key] = item;
  }
  return result;
}

function keyValueLinesText(items) {
  return Object.entries(items || {}).sort(([a], [b]) => a.localeCompare(b)).map(([key, value]) => `${key}=${value}`).join("\n");
}

function textPreview(value, limit = 110) {
  const text = (value || "").replace(/\s+/g, " ").trim();
  return text.length > limit ? `${text.slice(0, limit)}…` : text;
}

function downloadTextFile(fileName, content, type = "text/csv;charset=utf-8") {
  const blob = new Blob(["\ufeff", content], { type });
  const link = document.createElement("a");
  link.href = URL.createObjectURL(blob);
  link.download = fileName;
  link.click();
  setTimeout(() => URL.revokeObjectURL(link.href), 1000);
}

function fileAsBase64(file) {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result).split(",", 2)[1] || "");
    reader.onerror = () => reject(reader.error || new Error("读取文件失败"));
    reader.readAsDataURL(file);
  });
}

function formatDate(value) {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString("zh-CN", { hour12: false });
}

function formatBytes(value) {
  if (value < 1024) return `${value} B`;
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KiB`;
  return `${(value / 1024 / 1024).toFixed(1)} MiB`;
}

function currentAdapterID() {
  return byId("connection-adapter").value || "local-current";
}

function updateConnectionAdapterUI() {
  const manual = currentAdapterID() === "manual-export";
  const production = byId("connection-environment").value === "production";
  const productionRemote = production && !manual;
  if (manual) {
    byId("evaluation-base-url").value = "";
    byId("evaluation-api-key").value = "";
    byId("evaluation-kb-id").value = "";
    byId("evaluation-chat-id").value = "";
    byId("evaluation-rerank-id").value = "";
  }
  byId("remote-evaluation-fields").hidden = manual;
  byId("manual-evaluation-hint").hidden = !manual;
  byId("production-adapter-warning").hidden = !productionRemote;
  byId("connection-summary").textContent = manual
    ? "目标环境配置（手工模式仅保存 dataset ID 与部署说明）"
    : "目标环境配置（保存地址、资源 ID 与 API Key）";
  byId("save-connection-button").textContent = manual ? "保存手工环境配置" : "保存环境配置";
  byId("evaluation-base-url").required = !manual;
  byId("evaluation-api-key").required = !manual;
  byId("evaluation-kb-id").required = !manual;
  byId("evaluation-chat-id").required = !manual;
  byId("evaluation-rerank-id").required = !manual;
  byId("load-evaluation-resources-button").disabled = manual;
  byId("load-evaluation-resources-button").textContent = productionRemote ? "读取生产资源（实验性）" : "读取 WeKnora 资源";
  byId("check-compatibility-button").textContent = productionRemote ? "只读检查生产接口（实验性）" : "只读检查兼容性";
  byId("start-evaluation-button").textContent = manual
    ? "创建手工评测记录"
    : productionRemote ? "启动生产评测（实验性）" : "启动本地评测任务";
  byId("evaluation-mode-title").textContent = manual
    ? "记录外部 WeKnora 手工评测"
    : productionRemote ? "生产 WeKnora 远程评测（实验性）" : "发起本地 WeKnora 评测";
  byId("evaluation-mode-description").textContent = manual
    ? "工具只记录数据包部署和外部结果，不会连接目标 WeKnora。"
    : productionRemote
      ? "生产自动对接已暂停；这里只保留经过人工确认后的实验性入口。"
      : "local-current 仅用于与当前仓库契约一致的本地 WeKnora。";
  byId("evaluation-confirm-text").textContent = manual
    ? "我已按目标环境部署说明完成数据包部署，并核对 ZIP SHA-256 与 dataset ID。工具只创建本地记录，不会调用远程接口。"
    : "我已按目标环境部署说明完成数据包部署，核对 ZIP SHA-256 与 dataset ID，并确认本次评测会调用模型、产生费用。";
  byId("evaluation-resource-summary").textContent = "可读取资源，也可以手工填写下方 ID";
  updateExternalResultVisibility();
}

function resetConnectionForm() {
  byId("connection-profile-select").value = "";
  byId("connection-profile-id").value = "";
  byId("connection-profile-id").disabled = false;
  byId("connection-profile-name").value = "";
  byId("connection-environment").value = "local";
  byId("connection-adapter").value = "local-current";
  byId("connection-dataset-id").value = "default";
  byId("connection-deployment-notes").value = "";
  byId("evaluation-base-url").value = "http://127.0.0.1:8080";
  byId("evaluation-api-key").value = "";
  byId("evaluation-kb-id").value = "";
  byId("evaluation-chat-id").value = "";
  byId("evaluation-rerank-id").value = "";
  updateConnectionAdapterUI();
  state.compatibilityReport = null;
  renderCompatibilityReport();
}

function applyConnectionProfile(profile) {
  if (!profile) { resetConnectionForm(); return; }
  const manual = profile.adapter_id === "manual-export";
  byId("connection-profile-select").value = profile.id;
  byId("connection-profile-id").value = profile.id;
  byId("connection-profile-id").disabled = true;
  byId("connection-profile-name").value = profile.name;
  byId("connection-environment").value = profile.environment;
  byId("connection-adapter").value = profile.adapter_id;
  byId("connection-dataset-id").value = profile.dataset_id || "";
  byId("connection-deployment-notes").value = profile.deployment_notes || "";
  byId("evaluation-base-url").value = manual ? "" : (profile.base_url || "");
  byId("evaluation-api-key").value = manual ? "" : (profile.api_key || "");
  byId("evaluation-kb-id").value = manual ? "" : (profile.knowledge_base_id || "");
  byId("evaluation-chat-id").value = manual ? "" : (profile.chat_model_id || "");
  byId("evaluation-rerank-id").value = manual ? "" : (profile.rerank_model_id || "");
  updateConnectionAdapterUI();
  state.compatibilityReport = null;
  renderCompatibilityReport();
}

function connectionProfilePayload() {
  const manual = currentAdapterID() === "manual-export";
  return {
    id: byId("connection-profile-id").value.trim(),
    name: byId("connection-profile-name").value.trim(),
    environment: byId("connection-environment").value,
    adapter_id: currentAdapterID(),
    base_url: manual ? "" : byId("evaluation-base-url").value.trim(),
    api_key: manual ? "" : byId("evaluation-api-key").value.trim(),
    dataset_id: byId("connection-dataset-id").value.trim(),
    knowledge_base_id: manual ? "" : byId("evaluation-kb-id").value.trim(),
    chat_model_id: manual ? "" : byId("evaluation-chat-id").value.trim(),
    rerank_model_id: manual ? "" : byId("evaluation-rerank-id").value.trim(),
    deployment_notes: byId("connection-deployment-notes").value.trim(),
  };
}

async function loadConnectionProfiles(selectedID = byId("connection-profile-select").value) {
  const result = await api("/api/connections");
  state.connectionProfiles = result.data || [];
  const select = byId("connection-profile-select");
  select.replaceChildren();
  const empty = document.createElement("option"); empty.value = ""; empty.textContent = "新建配置"; select.append(empty);
  for (const profile of state.connectionProfiles) {
    const option = document.createElement("option"); option.value = profile.id;
    option.textContent = `${profile.name} · ${profile.environment} · ${profile.adapter_id}`;
    select.append(option);
  }
  const selected = state.connectionProfiles.find((item) => item.id === selectedID);
  if (selected) applyConnectionProfile(selected);
  renderConfigurationSelectors();
}

async function saveConnectionProfile() {
  const profile = connectionProfilePayload();
  if (!profile.id || !profile.name) throw new Error("请填写配置 ID 和配置名称");
  const result = await api(`/api/connections/${encodeURIComponent(profile.id)}`, {
    method: "PUT", headers: { "Content-Type": "application/json" }, body: JSON.stringify(profile),
  });
  await loadConnectionProfiles(result.data.id);
  return result.data;
}

function compatibilityStatusText(status) {
  return {
    passed: "通过", warning: "待验证", failed: "失败", unauthorized: "无权限",
    incompatible: "不兼容", not_supported: "不支持",
  }[status] || status;
}

function renderCompatibilityReport() {
  const container = byId("compatibility-report");
  container.replaceChildren();
  byId("download-compatibility-button").disabled = !state.compatibilityReport;
  if (!state.compatibilityReport) {
    container.className = "compatibility-report muted";
    container.textContent = "尚未运行兼容性检查";
    return;
  }
  const report = state.compatibilityReport;
  container.className = "compatibility-report";
  const summary = document.createElement("p");
  summary.textContent = `${report.environment} · ${report.adapter_id} · 通过 ${report.summary.passed} · 待验证 ${report.summary.warnings} · 失败 ${report.summary.failed + report.summary.incompatible} · 无权限 ${report.summary.unauthorized}`;
  container.append(summary);
  const sourceCapability = document.createElement("p");
  sourceCapability.className = "muted";
  sourceCapability.textContent = report.capabilities?.import_knowledge_chunks
    ? "知识库分块读取：Adapter 已声明支持；只有下方三项分块数据源检查全部通过，下一阶段的导入功能才会启用。"
    : "知识库分块读取：当前 Adapter 不支持；人工维护、CSV 导入和 ZIP 导出不受影响。";
  container.append(sourceCapability);
  const list = document.createElement("ul");
  for (const check of report.checks || []) {
    const item = document.createElement("li"); item.className = `compatibility-${check.status}`;
    const title = document.createElement("strong");
    title.textContent = `${check.name}：${compatibilityStatusText(check.status)}${check.http_status ? `（HTTP ${check.http_status}）` : ""}`;
    const message = document.createElement("span"); message.textContent = check.message;
    item.append(title, message); list.append(item);
  }
  container.append(list);
}

async function checkCompatibility() {
  const profileID = byId("connection-profile-select").value;
  if (!profileID) throw new Error("请先保存并选择一个环境配置");
  const apiKey = byId("evaluation-api-key").value.trim();
  if (currentAdapterID() !== "manual-export" && !apiKey) throw new Error("只读检查需要空间 API Key，请先填写并保存环境配置");
  const result = await api("/api/compatibility-checks", {
    method: "POST", headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ profile_id: profileID, api_key: apiKey }),
  });
  state.compatibilityReport = result.data;
  renderCompatibilityReport();
  return result.data;
}

function resetGenerationConnectionForm() {
  byId("generation-connection-select").value = "";
  byId("generation-connection-id").value = "";
  byId("generation-connection-id").disabled = false;
  byId("generation-connection-name").value = "";
  byId("generation-base-url").value = "";
  byId("generation-api-key").value = "";
  byId("generation-model").value = "";
  byId("generation-test-prompt").value = "回复1";
  byId("generation-timeout").value = "60";
  byId("generation-default-count").value = "1";
  byId("generation-max-output").value = "1200";
  byId("generation-max-total").value = "20000";
  byId("generation-input-price").value = "0";
  byId("generation-output-price").value = "0";
  updateGenerationEstimate();
}

function applyGenerationConnection(profile) {
  if (!profile) { resetGenerationConnectionForm(); return; }
  byId("generation-connection-select").value = profile.id;
  byId("generation-connection-id").value = profile.id;
  byId("generation-connection-id").disabled = true;
  byId("generation-connection-name").value = profile.name || "";
  byId("generation-base-url").value = profile.base_url || "";
  byId("generation-api-key").value = profile.api_key || "";
  byId("generation-model").value = profile.model || "";
  byId("generation-test-prompt").value = profile.test_prompt || "回复1";
  byId("generation-timeout").value = String(profile.timeout_seconds || 60);
  byId("generation-default-count").value = String(profile.questions_per_passage || 1);
  byId("generation-question-count").value = String(profile.questions_per_passage || 1);
  byId("generation-max-output").value = String(profile.max_output_tokens || 1200);
  byId("generation-max-total").value = String(profile.max_total_tokens || 20000);
  byId("generation-input-price").value = String(profile.input_price_per_million || 0);
  byId("generation-output-price").value = String(profile.output_price_per_million || 0);
  updateGenerationEstimate();
}

function generationConnectionPayload() {
  return {
    id: byId("generation-connection-id").value.trim(),
    name: byId("generation-connection-name").value.trim(),
    base_url: byId("generation-base-url").value.trim(),
    api_key: byId("generation-api-key").value.trim(),
    model: byId("generation-model").value.trim(),
    test_prompt: byId("generation-test-prompt").value.trim() || "回复1",
    timeout_seconds: Number(byId("generation-timeout").value),
    questions_per_passage: Number(byId("generation-default-count").value),
    max_output_tokens: Number(byId("generation-max-output").value),
    max_total_tokens: Number(byId("generation-max-total").value),
    input_price_per_million: Number(byId("generation-input-price").value),
    output_price_per_million: Number(byId("generation-output-price").value),
  };
}

async function loadGenerationConnections(selectedID = byId("generation-connection-select").value) {
  const response = await api("/api/generation-connections");
  state.generationConnections = response.data || [];
  const select = byId("generation-connection-select"); select.replaceChildren();
  const empty = document.createElement("option"); empty.value = ""; empty.textContent = "新建配置"; select.append(empty);
  for (const profile of state.generationConnections) {
    const option = document.createElement("option"); option.value = profile.id; option.textContent = `${profile.name} · ${profile.model}`; select.append(option);
  }
  const selected = state.generationConnections.find((item) => item.id === selectedID);
  if (selected) applyGenerationConnection(selected);
  renderConfigurationSelectors();
  updateGenerationEstimate();
}

async function saveGenerationConnection() {
  const profile = generationConnectionPayload();
  if (!profile.id || !profile.name) throw new Error("请填写生成配置 ID 和名称");
  const response = await api(`/api/generation-connections/${encodeURIComponent(profile.id)}`, {
    method: "PUT", headers: { "Content-Type": "application/json" }, body: JSON.stringify(profile),
  });
  await loadGenerationConnections(response.data.id);
  return response.data;
}

function replaceProfileOptions(select, profiles, emptyLabel, labeler) {
  const selected = select.value;
  select.replaceChildren();
  const empty = document.createElement("option"); empty.value = ""; empty.textContent = emptyLabel; select.append(empty);
  for (const profile of profiles) {
    const option = document.createElement("option"); option.value = profile.id; option.textContent = labeler(profile); select.append(option);
  }
  select.value = selected;
}

function renderConfigurationSelectors() {
  replaceProfileOptions(byId("default-generation-profile-select"), state.generationConnections, "不设置", (item) => `${item.name} · ${item.model}`);
  replaceProfileOptions(byId("default-evaluation-profile-select"), state.connectionProfiles, "不设置", (item) => `${item.name} · ${item.environment} · ${item.adapter_id}`);
  replaceProfileOptions(byId("dataset-generation-profile-select"), state.generationConnections, "未绑定", (item) => `${item.name} · ${item.model}`);
  replaceProfileOptions(byId("dataset-evaluation-profile-select"), state.connectionProfiles, "未绑定", (item) => `${item.name} · ${item.adapter_id}`);
  byId("default-generation-profile-select").value = state.settings.default_generation_profile_id || "";
  byId("default-evaluation-profile-select").value = state.settings.default_evaluation_profile_id || "";
  if (state.project) {
    byId("dataset-generation-profile-select").value = state.project.generation_profile_id || "";
    byId("dataset-evaluation-profile-select").value = state.project.evaluation_profile_id || "";
  }
  renderDatasetProfileSummaries();
  renderSourceProfileOptions();
}

function replaceSimpleOptions(select, items, emptyLabel, labeler) {
  const selected = select.value;
  select.replaceChildren();
  const empty = document.createElement("option"); empty.value = ""; empty.textContent = emptyLabel; select.append(empty);
  for (const item of items) {
    const option = document.createElement("option"); option.value = item.id; option.textContent = labeler(item); select.append(option);
  }
  select.value = items.some((item) => item.id === selected) ? selected : "";
}

function renderSourceProfileOptions() {
  const select = byId("source-profile-select");
  if (!select) return;
  const profiles = state.connectionProfiles.filter((item) => item.adapter_id !== "manual-export");
  const preferred = select.value || state.project?.evaluation_profile_id || "";
  replaceSimpleOptions(select, profiles, "请选择支持分块读取的环境", (item) => `${item.name} · ${item.environment} · ${item.adapter_id}`);
  if (profiles.some((item) => item.id === preferred)) select.value = preferred;
}

function resetSourceImport({ keepProfile = true } = {}) {
  const profileID = keepProfile ? byId("source-profile-select").value : "";
  state.sourceImport = {
    knowledgeBases: [], documents: [], chunks: [], selectedChunkIDs: new Set(),
    chunkPage: 1, chunkPageSize: 20, chunkTotal: 0, preview: null, importedPassageIDs: [],
  };
  if (!keepProfile) byId("source-profile-select").value = "";
  else byId("source-profile-select").value = profileID;
  replaceSimpleOptions(byId("source-kb-select"), [], "请先读取知识库", (item) => item.name);
  replaceSimpleOptions(byId("source-document-select"), [], "请先读取文档", (item) => item.title);
  byId("source-kb-select").disabled = true;
  byId("source-document-select").disabled = true;
  byId("load-source-documents").disabled = true;
  byId("load-source-chunks").disabled = true;
  byId("source-chunk-browser").hidden = true;
  byId("source-import-status").textContent = "请选择环境；生产环境必须先通过只读分块契约检查。";
  renderSourceChunks();
  renderSourceImportPreview();
}

function sourceProfile() {
  return state.connectionProfiles.find((item) => item.id === byId("source-profile-select").value);
}

async function loadSourceKnowledgeBases() {
  const profile = sourceProfile();
  if (!profile) throw new Error("请选择一个 WeKnora 环境配置");
  const response = await api(`/api/source-profiles/${encodeURIComponent(profile.id)}/knowledge-bases?page=1&page_size=100`);
  state.sourceImport.knowledgeBases = response.data.items || [];
  replaceSimpleOptions(byId("source-kb-select"), state.sourceImport.knowledgeBases, "请选择知识库", (item) => item.name || item.id);
  byId("source-kb-select").disabled = !state.sourceImport.knowledgeBases.length;
  const preferred = profile.knowledge_base_id;
  if (preferred && state.sourceImport.knowledgeBases.some((item) => item.id === preferred)) byId("source-kb-select").value = preferred;
  byId("load-source-documents").disabled = !byId("source-kb-select").value;
  byId("source-import-status").textContent = `已读取 ${state.sourceImport.knowledgeBases.length}/${response.data.total || state.sourceImport.knowledgeBases.length} 个知识库。远程请求未向浏览器暴露 API Key。`;
}

async function loadSourceDocuments() {
  const profileID = byId("source-profile-select").value;
  const kbID = byId("source-kb-select").value;
  if (!profileID || !kbID) throw new Error("请先选择知识库");
  const response = await api(`/api/source-profiles/${encodeURIComponent(profileID)}/knowledge-bases/${encodeURIComponent(kbID)}/knowledge?page=1&page_size=100`);
  state.sourceImport.documents = response.data.items || [];
  const select = byId("source-document-select");
  replaceSimpleOptions(select, state.sourceImport.documents, "请选择已解析文档", (item) => `${item.file_name || item.title || item.id} · ${item.parse_status || "状态未知"}`);
  for (const option of select.options) {
    if (!option.value) continue;
    const item = state.sourceImport.documents.find((doc) => doc.id === option.value);
    option.disabled = (item.parse_status && item.parse_status !== "completed") || (item.enable_status && item.enable_status !== "enabled");
  }
  select.disabled = !state.sourceImport.documents.length;
  byId("load-source-chunks").disabled = !select.value;
  byId("source-import-status").textContent = `已读取 ${state.sourceImport.documents.length}/${response.data.total || state.sourceImport.documents.length} 个文档；仅“completed 且 enabled”的文档可导入。`;
}

async function loadSourceChunks(page = 1) {
  const profileID = byId("source-profile-select").value;
  const knowledgeID = byId("source-document-select").value;
  if (!profileID || !knowledgeID) throw new Error("请先选择已解析文档");
  const pageSize = state.sourceImport.chunkPageSize;
  const response = await api(`/api/source-profiles/${encodeURIComponent(profileID)}/knowledge/${encodeURIComponent(knowledgeID)}/chunks?page=${page}&page_size=${pageSize}`);
  state.sourceImport.chunks = response.data.items || [];
  state.sourceImport.chunkPage = response.data.page || page;
  state.sourceImport.chunkTotal = response.data.total || state.sourceImport.chunks.length;
  state.sourceImport.preview = null;
  byId("source-chunk-browser").hidden = false;
  byId("source-import-status").textContent = `已读取文档分块，共 ${state.sourceImport.chunkTotal} 条。请勾选后先运行导入预检。`;
  renderSourceChunks();
  renderSourceImportPreview();
}

function sourceChunkSelectable(chunk) {
  return Boolean((chunk.content || "").trim()) && chunk.is_enabled !== false && (!chunk.chunk_type || chunk.chunk_type === "text");
}

function renderSourceChunks() {
  const body = byId("source-chunk-table-body");
  if (!body) return;
  body.replaceChildren();
  for (const chunk of state.sourceImport.chunks) {
    const row = document.createElement("tr");
    const selection = document.createElement("td");
    const checkbox = document.createElement("input"); checkbox.type = "checkbox"; checkbox.value = chunk.id;
    checkbox.checked = state.sourceImport.selectedChunkIDs.has(chunk.id); checkbox.disabled = !sourceChunkSelectable(chunk); selection.append(checkbox);
    const index = document.createElement("td"); index.textContent = String(chunk.chunk_index ?? "—");
    const content = document.createElement("td"); content.textContent = textPreview(chunk.content, 220);
    const length = document.createElement("td"); length.textContent = String((chunk.content || "").length);
    const status = document.createElement("td"); status.textContent = sourceChunkSelectable(chunk) ? (chunk.index_status || "可导入") : "不可导入";
    row.append(selection, index, content, length, status); body.append(row);
  }
  const pages = Math.max(1, Math.ceil(state.sourceImport.chunkTotal / state.sourceImport.chunkPageSize));
  byId("source-page-label").textContent = `第 ${state.sourceImport.chunkPage}/${pages} 页`;
  byId("source-prev-page").disabled = state.sourceImport.chunkPage <= 1;
  byId("source-next-page").disabled = state.sourceImport.chunkPage >= pages;
  byId("source-selection-count").textContent = `已选择 ${state.sourceImport.selectedChunkIDs.size} 个分块`;
}

function sourceImportPayload() {
  return {
    profile_id: byId("source-profile-select").value,
    knowledge_base_id: byId("source-kb-select").value,
    knowledge_id: byId("source-document-select").value,
    chunk_ids: [...state.sourceImport.selectedChunkIDs],
    changed_policy: byId("source-changed-policy").value,
  };
}

function renderSourceImportPreview() {
  const container = byId("source-import-preview");
  if (!container) return;
  container.replaceChildren();
  const preview = state.sourceImport.preview;
  byId("confirm-source-import").disabled = !preview?.summary?.importable;
  if (!preview) {
    container.className = "compatibility-report muted";
    container.textContent = "尚未运行导入预检";
    return;
  }
  container.className = "compatibility-report";
  const summary = document.createElement("p");
  summary.textContent = `选中 ${preview.summary.selected} · 可导入 ${preview.summary.importable} · 已存在 ${preview.summary.existing} · 内容变化 ${preview.summary.changed} · 无效 ${preview.summary.invalid}`;
  const list = document.createElement("ul");
  for (const item of preview.items || []) {
    const row = document.createElement("li"); row.className = item.status === "new" ? "compatibility-passed" : item.status === "invalid" ? "compatibility-incompatible" : "compatibility-warning";
    const title = document.createElement("strong"); title.textContent = `分块 ${item.chunk_index ?? "—"} · ${item.status}`;
    const message = document.createElement("span"); message.textContent = item.message || item.content_preview || "可导入";
    row.append(title, message); list.append(row);
  }
  container.append(summary, list);
}

async function previewSourceImport() {
  if (!state.project) throw new Error("请先打开数据集");
  const response = await api(`/api/datasets/${encodeURIComponent(state.project.id)}/passages/import-weknora-preview`, {
    method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(sourceImportPayload()),
  });
  state.sourceImport.preview = response.data;
  renderSourceImportPreview();
}

async function confirmSourceImport() {
  if (!state.sourceImport.preview?.summary?.importable) throw new Error("请先运行预检并确认有可导入语料");
  const count = state.sourceImport.preview.summary.importable;
  if (!confirm(`将向当前数据集新增 ${count} 条语料，确定继续吗？`)) return;
  const response = await api(`/api/datasets/${encodeURIComponent(state.project.id)}/passages/import-weknora`, {
    method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(sourceImportPayload()),
  });
  state.project = response.data.project;
  state.sourceImport.importedPassageIDs = response.data.imported_passage_ids || [];
  state.generationPassageSelection = new Set(state.sourceImport.importedPassageIDs);
  state.sourceImport.preview = null;
  setDirty(false); renderAll(); renderSourceImportPreview(); await loadDatasetList();
  byId("go-to-generation-after-import").hidden = !state.sourceImport.importedPassageIDs.length;
  showToast(`已导入 ${response.data.summary.imported} 条语料，跳过 ${response.data.summary.skipped} 条`);
}

function renderDatasetProfileSummaries() {
  const generation = state.generationConnections.find((item) => item.id === state.project?.generation_profile_id);
  byId("dataset-generation-profile-summary").textContent = generation
    ? `${generation.name} · ${generation.model} · ${generation.base_url}`
    : state.project?.generation_profile_id ? `配置 ${state.project.generation_profile_id} 在当前 workspace 中不存在，请重新绑定。` : "尚未绑定生成模型配置";
  const evaluation = state.connectionProfiles.find((item) => item.id === state.project?.evaluation_profile_id);
  byId("dataset-evaluation-profile-summary").textContent = evaluation
    ? `${evaluation.name} · ${evaluation.environment} · ${evaluation.adapter_id}${evaluation.dataset_id ? ` · dataset ${evaluation.dataset_id}` : ""}`
    : state.project?.evaluation_profile_id ? `配置 ${state.project.evaluation_profile_id} 在当前 workspace 中不存在，请重新绑定。` : "尚未绑定 WeKnora 环境配置";
}

function applyDatasetProfileBindings() {
  if (!state.project) return;
  if (!state.project.generation_profile_id && state.settings.default_generation_profile_id) {
    state.project.generation_profile_id = state.settings.default_generation_profile_id;
  }
  if (!state.project.evaluation_profile_id && state.settings.default_evaluation_profile_id) {
    state.project.evaluation_profile_id = state.settings.default_evaluation_profile_id;
  }
  renderConfigurationSelectors();
  const generation = state.generationConnections.find((item) => item.id === state.project.generation_profile_id);
  if (generation) applyGenerationConnection(generation);
  const evaluation = state.connectionProfiles.find((item) => item.id === state.project.evaluation_profile_id);
  if (evaluation) applyConnectionProfile(evaluation);
}

async function loadWorkspaceSettings() {
  const response = await api("/api/settings");
  state.settings = response.data;
  renderConfigurationSelectors();
  return state.settings;
}

async function saveWorkspaceSettings() {
  const response = await api("/api/settings", {
    method: "PUT", headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      schema_version: 1,
      default_generation_profile_id: byId("default-generation-profile-select").value,
      default_evaluation_profile_id: byId("default-evaluation-profile-select").value,
    }),
  });
  state.settings = response.data;
  renderConfigurationSelectors();
  return response.data;
}

async function configurationReferences(type, id) {
  const response = await api(`/api/configuration-references/${encodeURIComponent(type)}/${encodeURIComponent(id)}`);
  return response.data;
}

function renderGenerationPassageChoices() {
  const container = byId("generation-passage-choices");
  if (!container) return;
  container.replaceChildren();
  if (!state.project?.passages?.length) {
    const empty = document.createElement("p"); empty.className = "muted"; empty.textContent = "请先维护语料"; container.append(empty);
    updateGenerationEstimate(); return;
  }
  for (const passage of [...state.project.passages].sort((a, b) => a.id - b.id)) {
    const label = document.createElement("label"); label.className = "generation-passage-choice";
    const checkbox = document.createElement("input"); checkbox.type = "checkbox"; checkbox.value = String(passage.id); checkbox.checked = state.generationPassageSelection.has(passage.id);
    const id = document.createElement("span"); id.className = "choice-id"; id.textContent = `#${passage.id}`;
    const text = document.createElement("span"); text.textContent = `${passage.source || "未标来源"} · ${textPreview(passage.text, 180)}`;
    label.append(checkbox, id, text); container.append(label);
  }
  updateGenerationEstimate();
}

function estimateGenerationTokens() {
  const selected = state.project?.passages?.filter((item) => state.generationPassageSelection.has(item.id)) || [];
  const count = Number(byId("generation-question-count").value) || 1;
  const outputPerPassage = Number(byId("generation-max-output").value) || 1200;
  const input = selected.reduce((total, passage) => total + Math.ceil((passage.text.length + 900) / 2), 0);
  return { passages: selected.length, candidates: selected.length * count, input, output: selected.length * outputPerPassage };
}

function updateGenerationEstimate() {
  const estimate = estimateGenerationTokens();
  byId("generation-passage-count").textContent = `已选择 ${estimate.passages} 条`;
  const profile = state.generationConnections.find((item) => item.id === byId("generation-connection-select").value) || generationConnectionPayload();
  if (!estimate.passages || !profile.model) {
    byId("generation-estimate").textContent = "请选择生成配置和语料以查看任务规模。";
    return;
  }
  const cost = estimate.input / 1000000 * Number(profile.input_price_per_million || 0) + estimate.output / 1000000 * Number(profile.output_price_per_million || 0);
  byId("generation-estimate").textContent = `预计生成 ${estimate.candidates} 个候选 · 输入约 ${estimate.input} token · 输出上限 ${estimate.output} token · 费用上限估算 ${cost.toFixed(4)}。实际用量以模型响应为准。`;
}

function generationJobStatus(status) {
  return { pending: "等待中", running: "生成中", succeeded: "已完成", failed: "失败", canceled: "已取消" }[status] || status;
}

async function loadGenerationJobs(selectedID = byId("generation-job-select").value) {
  if (!state.project) return;
  const response = await api(`/api/datasets/${encodeURIComponent(state.project.id)}/generation-jobs`);
  state.generationJobs = response.data || [];
  const select = byId("generation-job-select"); select.replaceChildren();
  const empty = document.createElement("option"); empty.value = ""; empty.textContent = state.generationJobs.length ? "请选择任务" : "暂无任务"; select.append(empty);
  for (const job of state.generationJobs) {
    const option = document.createElement("option"); option.value = job.id;
    option.textContent = `${formatDate(job.created_at)} · ${generationJobStatus(job.status)} · ${job.completed_passages}/${job.total_passages} 语料 · ${job.candidates?.length || 0} 候选`;
    select.append(option);
  }
  const chosen = state.generationJobs.find((item) => item.id === selectedID);
  if (chosen) {
    select.value = chosen.id; state.generationJob = chosen; renderGenerationJob(chosen);
  }
}

function renderGenerationJob(job) {
  state.generationJob = job;
  const summary = byId("generation-job-summary");
  summary.className = "generation-job-summary";
  summary.textContent = `${generationJobStatus(job.status)} · 语料 ${job.completed_passages}/${job.total_passages} · 失败 ${job.failed_passages || 0} · 缓存命中 ${job.cache_hits || 0} · 候选 ${job.candidates?.length || 0} · token ${job.usage?.total_tokens || 0}/${job.token_budget || 0} · 费用估算 ${Number(job.actual_estimated_cost || 0).toFixed(4)}${job.errors?.length ? ` · ${job.errors.join("；")}` : ""}`;
  byId("cancel-generation-job-button").disabled = !["pending", "running"].includes(job.status);
  const actions = byId("generation-candidate-actions"); actions.hidden = !(job.candidates || []).length;
  const container = byId("generation-candidates"); container.replaceChildren();
  for (const candidate of job.candidates || []) {
    const card = document.createElement("article"); card.className = `generation-candidate${candidate.review_status !== "pending" ? " reviewed" : ""}`; card.dataset.id = candidate.id;
    const checkbox = document.createElement("input"); checkbox.type = "checkbox"; checkbox.className = "generation-candidate-select"; checkbox.disabled = candidate.review_status !== "pending";
    const content = document.createElement("div");
    const meta = document.createElement("div"); meta.className = "generation-candidate-meta";
    meta.textContent = `${candidate.id} · 来源语料 ${candidate.passage_ids.join(", ")} · ${candidate.review_status === "pending" ? "待审核" : candidate.review_status === "approved" ? `已批准为问题 #${candidate.approved_question_id}` : "已拒绝"}`;
    const fields = document.createElement("div"); fields.className = "generation-candidate-fields";
    const addField = (labelText, field, value, rows = 0, wide = false) => {
      const label = document.createElement("label"); if (wide) label.className = "wide"; label.textContent = labelText;
      const input = rows ? document.createElement("textarea") : document.createElement("input");
      if (rows) input.rows = rows; input.value = value || ""; input.dataset.field = field; input.disabled = candidate.review_status !== "pending"; label.append(input); fields.append(label);
    };
    addField("问题", "question", candidate.question, 2, true);
    addField("标准答案", "answer", candidate.answer, 4, true);
    addField("答案核心要点（每行一个）", "answer_key_points", lineListText(candidate.answer_key_points), 3);
    addField("证据原文（每行一个）", "evidence_quotes", lineListText(candidate.evidence_quotes), 3);
    addField("分类", "category", candidate.category);
    addField("难度", "difficulty", candidate.difficulty);
    addField("标签（逗号分隔）", "tags", tagsText(candidate.tags));
    const issues = document.createElement("ul"); issues.className = "generation-candidate-issues";
    for (const issue of candidate.issues || []) {
      const item = document.createElement("li"); item.className = issue.severity; item.textContent = `${issue.severity === "error" ? "阻断" : "提示"}：${issue.message}`; issues.append(item);
    }
    if (!(candidate.issues || []).length) {
      const item = document.createElement("li"); item.textContent = "确定性校验通过"; issues.append(item);
    }
    content.append(meta, fields, issues); card.append(checkbox, content); container.append(card);
  }
  scheduleGenerationPolling(job);
}

function scheduleGenerationPolling(job) {
  clearTimeout(state.generationPollTimer); state.generationPollTimer = null;
  if (!["pending", "running"].includes(job.status) || !state.project) return;
  state.generationPollTimer = setTimeout(async () => {
    try { await loadGenerationJob(job.id); } catch (error) { showToast(error.message, true); }
  }, 2000);
}

async function loadGenerationJob(jobID = byId("generation-job-select").value) {
  if (!state.project || !jobID) throw new Error("请选择生成任务");
  const response = await api(`/api/datasets/${encodeURIComponent(state.project.id)}/generation-jobs/${encodeURIComponent(jobID)}`);
  byId("generation-job-select").value = jobID;
  renderGenerationJob(response.data);
  return response.data;
}

function selectedGenerationCandidateReviews(action) {
  const items = [];
  for (const card of byId("generation-candidates").querySelectorAll(".generation-candidate")) {
    const checkbox = card.querySelector(".generation-candidate-select");
    if (!checkbox?.checked) continue;
    const value = (field) => card.querySelector(`[data-field="${field}"]`)?.value || "";
    items.push({
      candidate_id: card.dataset.id, action,
      question: value("question"), answer: value("answer"),
      answer_key_points: parseLineList(value("answer_key_points")), evidence_quotes: parseLineList(value("evidence_quotes")),
      category: value("category"), difficulty: value("difficulty"), tags: parseTags(value("tags")),
    });
  }
  return items;
}

async function reviewGenerationCandidates(action) {
  if (!state.project || !state.generationJob) throw new Error("请先查看一个生成任务");
  const items = selectedGenerationCandidateReviews(action);
  if (!items.length) throw new Error("请先勾选候选");
  const response = await api(`/api/datasets/${encodeURIComponent(state.project.id)}/generation-jobs/${encodeURIComponent(state.generationJob.id)}/review`, {
    method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ items }),
  });
  state.project = response.data.project;
  setDirty(false); renderAll(); renderGenerationJob(response.data.job);
  await loadDatasetList(); await loadGenerationJobs(response.data.job.id);
}

function makeButton(label, action, id, danger = false) {
  const button = document.createElement("button");
  button.type = "button";
  button.textContent = label;
  button.dataset.action = action;
  button.dataset.id = String(id);
  if (danger) button.classList.add("danger");
  return button;
}

async function loadDatasetList() {
  const result = await api("/api/datasets");
  state.datasets = result.data || [];
  renderDatasetList();
}

function renderDatasetList() {
  datasetList.replaceChildren();
  if (!state.datasets.length) {
    const hint = document.createElement("p");
    hint.className = "muted";
    hint.textContent = "暂无数据集";
    datasetList.append(hint);
    return;
  }
  for (const item of state.datasets) {
    const button = document.createElement("button");
    button.type = "button";
    button.className = "dataset-item";
    button.classList.toggle("active", item.id === state.project?.id);
    button.dataset.id = item.id;
    const name = document.createElement("strong");
    name.textContent = item.name;
    const meta = document.createElement("span");
    meta.textContent = `${item.version} · ${item.question_count} 问题 · ${item.passage_count} 语料`;
    button.append(name, meta);
    datasetList.append(button);
  }
}

async function openDataset(id, force = false) {
  if (!force && state.dirty && !confirm("当前数据集有未保存修改，仍然切换吗？")) return;
  const result = await api(`/api/datasets/${encodeURIComponent(id)}`);
  state.project = result.data;
  state.editingPassageID = null;
  state.editingQuestionID = null;
  state.generationPassageSelection = new Set();
  state.generationJob = null;
  emptyState.hidden = true;
  editor.hidden = false;
  byId("meta-name").value = state.project.name || "";
  byId("meta-version").value = state.project.version || "";
  byId("meta-description").value = state.project.description || "";
  applyDatasetProfileBindings();
  byId("backup-link").href = `/api/datasets/${encodeURIComponent(id)}/backup`;
  resetPassageForm();
  resetQuestionForm();
  byId("validation-summary").className = "validation-summary muted";
  byId("validation-summary").textContent = "尚未运行校验";
  byId("validation-issues").replaceChildren();
  byId("statistics").replaceChildren();
  byId("statistics").hidden = true;
  setDirty(false);
  renderAll();
  renderSourceProfileOptions();
  if (state.connectionProfiles.some((item) => item.id === state.project.evaluation_profile_id && item.adapter_id !== "manual-export")) {
    byId("source-profile-select").value = state.project.evaluation_profile_id;
  }
  resetSourceImport();
  loadHistory().catch((error) => showToast(error.message, true));
  loadGenerationJobs().catch((error) => showToast(error.message, true));
}

function syncMetadata() {
  if (!state.project) return;
  state.project.name = byId("meta-name").value;
  state.project.version = byId("meta-version").value;
  state.project.description = byId("meta-description").value;
}

async function saveProject() {
  if (!state.project) return;
  syncMetadata();
  const result = await api(`/api/datasets/${encodeURIComponent(state.project.id)}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(state.project),
  });
  state.project = result.data;
  setDirty(false);
  await loadDatasetList();
  showToast("项目已保存");
}

function renderAll() {
  renderDatasetList();
  renderPassages();
  renderQuestions();
  renderPassageChoices();
  renderGenerationPassageChoices();
  byId("passage-count").textContent = `(${state.project?.passages?.length || 0})`;
  byId("question-count").textContent = `(${state.project?.questions?.length || 0})`;
}

function renderPassages() {
  const tbody = byId("passage-table");
  tbody.replaceChildren();
  if (!state.project) return;
  const filter = byId("passage-search").value.trim().toLowerCase();
  const passages = [...state.project.passages].sort((a, b) => a.id - b.id).filter((passage) => {
    const haystack = `${passage.text} ${passage.source || ""} ${tagsText(passage.tags)}`.toLowerCase();
    return !filter || haystack.includes(filter);
  });
  for (const passage of passages) {
    const row = document.createElement("tr");
    const id = document.createElement("td"); id.textContent = String(passage.id);
    const text = document.createElement("td"); text.textContent = textPreview(passage.text); text.className = "preview";
    const source = document.createElement("td"); source.textContent = passage.source || "—";
    const actions = document.createElement("td"); actions.className = "row-actions";
    actions.append(makeButton("编辑", "edit-passage", passage.id), makeButton("删除", "delete-passage", passage.id, true));
    row.append(id, text, source, actions);
    tbody.append(row);
  }
}

function resetPassageForm() {
  state.editingPassageID = null;
  byId("passage-form-title").textContent = "新增语料";
  byId("passage-form").reset();
}

function editPassage(id) {
  const passage = state.project.passages.find((item) => item.id === id);
  if (!passage) return;
  state.editingPassageID = id;
  byId("passage-form-title").textContent = `编辑语料 #${id}`;
  byId("passage-text").value = passage.text;
  byId("passage-source").value = passage.source || "";
  byId("passage-tags").value = tagsText(passage.tags);
  byId("passage-review-state").value = passage.review_state || "";
}

function deletePassage(id) {
  const references = state.project.questions.filter((question) => question.relevant_passage_ids.includes(id));
  if (references.length) {
    alert(`该语料仍被问题 ${references.map((item) => `#${item.id}`).join(", ")} 引用，请先解除关系。`);
    return;
  }
  if (!confirm(`确定删除语料 #${id}？`)) return;
  state.project.passages = state.project.passages.filter((item) => item.id !== id);
  if (state.editingPassageID === id) resetPassageForm();
  setDirty();
  renderAll();
}

function renderQuestions() {
  const tbody = byId("question-table");
  tbody.replaceChildren();
  if (!state.project) return;
  const filter = byId("question-search").value.trim().toLowerCase();
  const questions = [...state.project.questions].sort((a, b) => a.id - b.id).filter((question) => {
    const haystack = `${question.text} ${question.answer} ${question.category || ""} ${question.test_role || ""} ${question.annotation_source || ""} ${lineListText(question.answer_key_points)} ${lineListText(question.expected_documents)}`.toLowerCase();
    return !filter || haystack.includes(filter);
  });
  for (const question of questions) {
    const row = document.createElement("tr");
    const id = document.createElement("td"); id.textContent = String(question.id);
    const text = document.createElement("td"); text.textContent = textPreview(question.text); text.className = "preview";
    const relations = document.createElement("td"); relations.textContent = String(question.relevant_passage_ids.length);
    const actions = document.createElement("td"); actions.className = "row-actions";
    actions.append(makeButton("编辑", "edit-question", question.id), makeButton("删除", "delete-question", question.id, true));
    row.append(id, text, relations, actions);
    tbody.append(row);
  }
}

function selectedPassageIDs() {
  return [...state.questionPassageSelection].sort((a, b) => a - b);
}

function renderPassageChoices(selected) {
  const container = byId("passage-choices");
  if (!container || !state.project) return;
  if (selected) state.questionPassageSelection = new Set(selected);
  const selectedIDs = state.questionPassageSelection;
  const filter = byId("relation-search").value.trim().toLowerCase();
  container.replaceChildren();
  for (const passage of [...state.project.passages].sort((a, b) => a.id - b.id)) {
    const haystack = `${passage.text} ${passage.source || ""}`.toLowerCase();
    if (filter && !haystack.includes(filter)) continue;
    const label = document.createElement("label"); label.className = "choice";
    const checkbox = document.createElement("input");
    checkbox.type = "checkbox"; checkbox.value = String(passage.id); checkbox.checked = selectedIDs.has(passage.id);
    const id = document.createElement("span"); id.className = "choice-id"; id.textContent = `#${passage.id}`;
    const text = document.createElement("span"); text.className = "choice-text"; text.textContent = textPreview(passage.text, 150);
    label.append(checkbox, id, text);
    container.append(label);
  }
  if (!state.project.passages.length) {
    const hint = document.createElement("p"); hint.className = "muted"; hint.textContent = "请先创建语料"; container.append(hint);
  }
}

function resetQuestionForm() {
  state.editingQuestionID = null;
  state.questionPassageSelection = new Set();
  byId("question-form-title").textContent = "新增问题";
  byId("question-form").reset();
  byId("question-dataset-version").value = state.project?.version || "";
  byId("question-feedback-provenance").hidden = true;
  byId("question-feedback-provenance").textContent = "";
  renderPassageChoices(new Set());
}

function editQuestion(id) {
  const question = state.project.questions.find((item) => item.id === id);
  if (!question) return;
  state.editingQuestionID = id;
  byId("question-form-title").textContent = `编辑问题 #${id}`;
  byId("question-text").value = question.text;
  byId("question-answer").value = question.answer;
  byId("question-category").value = question.category || "";
  byId("question-difficulty").value = question.difficulty || "";
  byId("question-tags").value = tagsText(question.tags);
  byId("question-review-state").value = question.review_state || "";
  byId("question-answer-key-points").value = lineListText(question.answer_key_points);
  byId("question-answerable").value = question.answerable === true ? "true" : question.answerable === false ? "false" : "";
  byId("question-expected-documents").value = lineListText(question.expected_documents);
  byId("question-forbidden-documents").value = lineListText(question.forbidden_documents);
  byId("question-test-role").value = question.test_role || "";
  byId("question-retrieval-filters").value = keyValueLinesText(question.retrieval_filters);
  byId("question-dataset-version").value = question.dataset_version || "";
  byId("question-annotation-source").value = question.annotation_source || "";
  const provenance = byId("question-feedback-provenance");
  provenance.hidden = !question.source_run_id;
  provenance.textContent = question.source_run_id ? `回流来源：运行 ${question.source_run_id} · 数据集版本 ${question.source_dataset_version || "未知"} · 原因 ${question.failure_reason || "未记录"}` : "";
  renderPassageChoices(new Set(question.relevant_passage_ids));
}

function deleteQuestion(id) {
  if (!confirm(`确定删除问题 #${id}？`)) return;
  state.project.questions = state.project.questions.filter((item) => item.id !== id);
  if (state.editingQuestionID === id) resetQuestionForm();
  setDirty();
  renderAll();
}

async function runValidation() {
  if (!state.project) return null;
  if (state.dirty) await saveProject();
  const result = await api(`/api/datasets/${encodeURIComponent(state.project.id)}/validate`, { method: "POST" });
  renderValidation(result.data);
  return result.data;
}

function renderValidation(report) {
  const summary = byId("validation-summary");
  summary.className = `validation-summary ${report.valid ? "valid" : "invalid"}`;
  summary.textContent = report.valid
    ? `校验通过：${report.warning_count} 个警告，可以导出。`
    : `校验未通过：${report.error_count} 个错误，${report.warning_count} 个警告。`;
  const list = byId("validation-issues");
  list.replaceChildren();
  for (const issue of report.issues) {
    const item = document.createElement("li"); item.className = issue.severity;
    const message = document.createElement("div"); message.textContent = issue.message;
    const code = document.createElement("div"); code.className = "issue-code";
    code.textContent = `${issue.code}${issue.field ? ` · ${issue.field}` : ""}`;
    item.append(message, code); list.append(item);
  }
  renderStatistics(report.statistics);
}

function renderStatistics(stats) {
  const container = byId("statistics");
  container.replaceChildren();
  if (!stats) {
    container.hidden = true;
    return;
  }
  const cards = [
    [stats.passage_count, "语料数"],
    [stats.question_count, "问题数"],
    [stats.qrel_count, "相关性关系"],
    [`${stats.passage_coverage_percent}%`, "语料覆盖率"],
    [stats.average_relations, "平均相关语料数"],
    [stats.average_passage_length, "平均语料字符数"],
    [stats.average_question_length, "平均问题字符数"],
    [stats.average_answer_length, "平均答案字符数"],
  ];
  for (const [value, label] of cards) {
    const card = document.createElement("div"); card.className = "stat-card";
    const strong = document.createElement("strong"); strong.textContent = String(value);
    const span = document.createElement("span"); span.textContent = label;
    card.append(strong, span); container.append(card);
  }
  for (const [label, distribution] of [["分类分布", stats.categories], ["难度分布", stats.difficulties], ["来源分布", stats.sources], ["审核状态", stats.review_states]]) {
    const card = document.createElement("div"); card.className = "stat-card distribution";
    const strong = document.createElement("strong"); strong.textContent = label;
    const span = document.createElement("span");
    span.textContent = Object.entries(distribution || {}).sort((a, b) => b[1] - a[1]).map(([key, count]) => `${key} ${count}`).join(" · ") || "暂无";
    card.append(strong, span); container.append(card);
  }
  container.hidden = false;
}

async function loadHistory() {
  if (!state.project) return;
  const projectID = state.project.id;
  const id = encodeURIComponent(projectID);
  const [snapshots, exports, evaluations] = await Promise.all([
    api(`/api/datasets/${id}/snapshots`),
    api(`/api/datasets/${id}/exports`),
    api(`/api/datasets/${id}/evaluations`),
  ]);
  if (state.project?.id !== projectID) return;
  renderSnapshots(snapshots.data || []);
  renderExportHistory(exports.data || []);
  renderEvaluationExportOptions(exports.data || []);
  state.evaluationRuns = evaluations.data || [];
  renderEvaluationRuns();
}

function renderSnapshots(items) {
  const container = byId("snapshot-list");
  container.replaceChildren();
  if (!items.length) {
    const empty = document.createElement("p"); empty.className = "muted"; empty.textContent = "暂无快照"; container.append(empty);
    return;
  }
  for (const item of items) {
    const row = document.createElement("div"); row.className = "history-item";
    const info = document.createElement("div");
    const title = document.createElement("strong"); title.textContent = `${item.version} · ${formatDate(item.created_at)}`;
    const meta = document.createElement("span"); meta.textContent = `${item.question_count} 问题 · ${item.passage_count} 语料`;
    info.append(title, meta);
    const button = makeButton("恢复", "restore-snapshot", item.id);
    row.append(info, button); container.append(row);
  }
}

function renderExportHistory(items) {
  const container = byId("export-history-list");
  container.replaceChildren();
  if (!items.length) {
    const empty = document.createElement("p"); empty.className = "muted"; empty.textContent = "暂无导出记录"; container.append(empty);
    return;
  }
  for (const item of items) {
    const row = document.createElement("div"); row.className = "history-item";
    const info = document.createElement("div");
    const title = document.createElement("strong"); title.textContent = formatDate(item.created_at);
    const meta = document.createElement("span"); meta.textContent = `${item.question_count} 问题 · ${item.passage_count} 语料 · ${formatBytes(item.size)}`;
    const contract = document.createElement("span");
    contract.textContent = `${item.contract_profile_id || "未知契约"} · SHA-256 ${(item.sha256 || "未记录").slice(0, 16)}${item.sha256 ? "…" : ""}`;
    const deployment = document.createElement("span");
    deployment.textContent = item.deployment_status === "deployed"
      ? `已部署 · ${item.environment || "未知环境"} · ${item.adapter_id || "未知 Adapter"} · dataset ${item.dataset_id || "未记录"}`
      : item.deployment_status === "not_deployed" ? "尚未部署" : "旧记录：部署状态未知";
    info.append(title, meta, contract, deployment);
    const actions = document.createElement("div"); actions.className = "toolbar";
    const link = document.createElement("a"); link.className = "button-link"; link.textContent = "下载";
    link.href = `/api/datasets/${encodeURIComponent(state.project.id)}/exports/${encodeURIComponent(item.id)}`;
    actions.append(link);
    actions.append(makeButton(item.deployment_status === "deployed" ? "标记未部署" : "记录已部署", item.deployment_status === "deployed" ? "mark-export-not-deployed" : "mark-export-deployed", item.id));
    row.append(info, actions); container.append(row);
  }
}

function renderEvaluationExportOptions(items) {
  const select = byId("evaluation-export");
  const previous = select.value;
  select.replaceChildren();
  const placeholder = document.createElement("option"); placeholder.value = ""; placeholder.textContent = items.length ? "选择已部署的导出版本" : "请先导出数据集";
  select.append(placeholder);
  for (const item of items) {
    const option = document.createElement("option"); option.value = item.id;
    option.textContent = `${formatDate(item.created_at)} · ${item.question_count} 问题 · ${item.deployment_status === "deployed" ? `已部署 ${item.dataset_id}` : "未登记部署"} · ${item.file_name}`;
    select.append(option);
  }
  if (items.some((item) => item.id === previous)) select.value = previous;
}

function renderEvaluationResourceOptions(listID, inputID, items) {
  const list = byId(listID);
  list.replaceChildren();
  for (const item of items) {
    const option = document.createElement("option");
    option.value = item.id;
    option.label = item.name === item.id ? item.id : `${item.name} · ${item.id}`;
    list.append(option);
  }
  const input = byId(inputID);
  if (!input.value && items.length === 1) input.value = items[0].id;
}

async function loadEvaluationResources() {
  const baseURL = byId("evaluation-base-url").value.trim();
  const apiKey = byId("evaluation-api-key").value.trim();
  if (!baseURL || !apiKey) throw new Error("请先填写 WeKnora 地址和空间 API Key");
  const result = await api("/api/evaluation-resources", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ adapter_id: currentAdapterID(), base_url: baseURL, api_key: apiKey }),
  });
  const resources = result.data;
  renderEvaluationResourceOptions("evaluation-kb-options", "evaluation-kb-id", resources.knowledge_bases || []);
  renderEvaluationResourceOptions("evaluation-chat-options", "evaluation-chat-id", resources.chat_models || []);
  renderEvaluationResourceOptions("evaluation-rerank-options", "evaluation-rerank-id", resources.rerank_models || []);
  const counts = `${resources.knowledge_bases.length} 个知识库 · ${resources.chat_models.length} 个对话模型 · ${resources.rerank_models.length} 个重排模型`;
  byId("evaluation-resource-summary").textContent = `已读取：${counts}`;
  return counts;
}

function evaluationStatus(status) {
  return ["等待中", "运行中", "已成功", "已失败"][status] || `未知状态 ${status}`;
}

function metricDisplayName(name) {
  const raw = String(name || "");
  const key = raw.toLowerCase().replace(/[_-]/g, "");
  const chinese = {
    precision: "精确率",
    recall: "召回率",
    hit: "命中率",
    mrr: "平均倒数排名",
    map: "平均精确率均值",
    bleu1: "一元生成相似度",
    bleu2: "二元生成相似度",
    bleu4: "四元生成相似度",
    rouge1: "一元词重叠率",
    rouge2: "二元词重叠率",
    rougel: "最长公共子序列重叠率",
  };
  let label = chinese[key];
  const ndcg = key.match(/^ndcg(\d+)$/);
  if (!label && ndcg) label = `归一化折损累计增益@${ndcg[1]}`;
  return label ? `${raw}（${label}）` : raw;
}

function metricSummary(run) {
  if (!run.metric) return "尚无指标";
  const retrieval = run.metric.retrieval_metrics || {};
  const generation = run.metric.generation_metrics || {};
  const values = [];
  for (const key of ["precision", "recall", "mrr", "map"]) {
    if (Object.hasOwn(retrieval, key)) values.push(`${key} ${Number(retrieval[key]).toFixed(4)}`);
  }
  for (const key of ["rouge1", "rougel", "bleu1"]) {
    if (Object.hasOwn(generation, key)) values.push(`${key} ${Number(generation[key]).toFixed(4)}`);
  }
  return values.join(" · ") || "指标为空";
}

function appendDetailField(container, label, value) {
  const item = document.createElement("div");
  const name = document.createElement("span"); name.textContent = label;
  const content = document.createElement("strong"); content.textContent = value == null || value === "" ? "—" : String(value);
  item.append(name, content); container.append(item);
}

function appendMetricSection(container, title, metrics) {
  const section = document.createElement("section");
  const heading = document.createElement("h4"); heading.textContent = title; section.append(heading);
  const grid = document.createElement("div"); grid.className = "evaluation-metric-grid";
  const entries = Object.entries(metrics || {}).sort(([left], [right]) => left.localeCompare(right));
  if (!entries.length) {
    const empty = document.createElement("p"); empty.className = "muted-text"; empty.textContent = "暂无指标"; section.append(empty);
  } else {
    for (const [name, value] of entries) appendDetailField(grid, metricDisplayName(name), Number(value).toFixed(4));
    section.append(grid);
  }
  container.append(section);
}

function buildEvaluationRunDetail(run) {
  const detail = document.createElement("div"); detail.className = "evaluation-run-detail"; detail.hidden = true;
  const fields = document.createElement("div"); fields.className = "evaluation-detail-grid";
  appendDetailField(fields, "记录 ID", run.id);
  appendDetailField(fields, "任务 ID", run.task_id);
  appendDetailField(fields, "状态", run.adapter_id === "manual-export" && run.status === 0 ? "等待外部结果" : evaluationStatus(run.status));
  appendDetailField(fields, "执行进度", `${run.finished || 0}/${run.total || 0}`);
  appendDetailField(fields, "创建时间", formatDate(run.created_at));
  appendDetailField(fields, "开始时间", run.start_time ? formatDate(run.start_time) : "—");
  appendDetailField(fields, "更新时间", formatDate(run.updated_at));
  appendDetailField(fields, "结果来源", run.result_source || "—");
  appendDetailField(fields, "环境", run.environment || "legacy");
  appendDetailField(fields, "Adapter", run.adapter_id || "local-current");
  appendDetailField(fields, "WeKnora API", run.base_url || "—");
  appendDetailField(fields, "dataset ID", run.dataset_id || "—");
  appendDetailField(fields, "知识库 ID", run.knowledge_base_id || "—");
  appendDetailField(fields, "对话模型 ID", run.chat_id || "—");
  appendDetailField(fields, "重排模型 ID", run.rerank_id || "—");
  appendDetailField(fields, "导出记录 ID", run.export_id || "—");
  appendDetailField(fields, "导出 SHA-256", run.export_sha256 || "—");
  appendDetailField(fields, "数据包契约", run.contract_profile_id || "—");
  appendDetailField(fields, "导出问题数", (run.export_question_ids || []).length);
  appendDetailField(fields, "逐题结果数", (run.external_items || []).length);
  detail.append(fields);

  const exported = (run.export_question_ids || []).length;
  if (exported && run.total && exported !== run.total) {
    const warning = document.createElement("p"); warning.className = "evaluation-detail-warning";
    warning.textContent = `数据量不一致：关联导出版本包含 ${exported} 道题，远程任务实际报告 ${run.total} 道题。请检查目标环境的 dataset ${run.dataset_id || "—"} 是否部署了所选导出版本。`;
    detail.append(warning);
  }
  if (!(run.external_items || []).length) {
    const warning = document.createElement("p"); warning.className = "evaluation-detail-note";
    warning.textContent = "该记录只有汇总指标，没有逐题实际答案、召回片段或耗时；生产接口未返回这些内容。";
    detail.append(warning);
  }
  if (run.error_message) {
    const error = document.createElement("p"); error.className = "evaluation-detail-error"; error.textContent = `错误：${run.error_message}`; detail.append(error);
  }
  appendMetricSection(detail, "检索指标", run.metric?.retrieval_metrics);
  appendMetricSection(detail, "生成指标", run.metric?.generation_metrics);
  const questions = document.createElement("section"); questions.className = "evaluation-detail-questions";
  const heading = document.createElement("h4"); heading.textContent = "关联导出题目";
  const loading = document.createElement("p"); loading.className = "muted-text"; loading.textContent = "展开详情后加载问题与标准答案…";
  questions.append(heading, loading); detail.append(questions);
  return detail;
}

async function loadEvaluationRunQuestions(runID, detail) {
  const container = detail.querySelector(".evaluation-detail-questions");
  if (!container || container.dataset.loaded === "true" || container.dataset.loading === "true") return;
  container.dataset.loading = "true";
  try {
    const response = await api(`/api/datasets/${encodeURIComponent(state.project.id)}/evaluations/${encodeURIComponent(runID)}/report`);
    const report = response.data;
    container.replaceChildren();
    const heading = document.createElement("h4"); heading.textContent = `关联导出题目（${report.items.length}）`;
    const note = document.createElement("p"); note.className = "muted-text";
    note.textContent = "题目和标准答案来自该记录关联的历史导出包；实际答案为空时，无法据此确认远程任务具体执行了哪些题。";
    const wrap = document.createElement("div"); wrap.className = "table-wrap evaluation-detail-question-table";
    const table = document.createElement("table");
    const head = document.createElement("thead"); const headRow = document.createElement("tr");
    for (const text of ["ID", "问题", "标准答案", "实际答案", "结果状态"]) {
      const cell = document.createElement("th"); cell.textContent = text; headRow.append(cell);
    }
    head.append(headRow); table.append(head);
    const body = document.createElement("tbody");
    for (const item of report.items || []) {
      const row = document.createElement("tr");
      for (const value of [item.question_id, item.question, item.standard_answer, item.actual_answer || "—", item.state || "—"]) {
        const cell = document.createElement("td"); cell.textContent = String(value); row.append(cell);
      }
      body.append(row);
    }
    table.append(body); wrap.append(table); container.append(heading, note, wrap);
    container.dataset.loaded = "true";
  } catch (error) {
    const message = container.querySelector("p") || document.createElement("p");
    message.className = "evaluation-detail-error"; message.textContent = `关联题目加载失败：${error.message}`;
    if (!message.parentNode) container.append(message);
  } finally {
    container.dataset.loading = "false";
  }
}

function renderEvaluationRuns() {
  const container = byId("evaluation-run-list");
  container.replaceChildren();
  if (!state.evaluationRuns.length) {
    const empty = document.createElement("p"); empty.className = "muted"; empty.textContent = "暂无评测记录"; container.append(empty);
    renderExternalResultOptions();
    renderReportOptions();
    renderComparisonOptions();
    return;
  }
  for (const run of state.evaluationRuns) {
    const row = document.createElement("div"); row.className = "history-item evaluation-run";
    const info = document.createElement("div");
    const title = document.createElement("strong");
    title.textContent = run.adapter_id === "manual-export"
      ? `${formatDate(run.created_at)} · 手工评测 · dataset ${run.dataset_id || "未记录"}`
      : `${formatDate(run.created_at)} · ${run.knowledge_base_id}`;
    const progress = document.createElement("span");
    progress.textContent = `${run.task_id} · ${run.finished || 0}/${run.total || 0}`;
    const trace = document.createElement("span");
    trace.textContent = `${run.environment || "legacy"} · ${run.adapter_id || "local-current"} · dataset ${run.dataset_id || "未记录"} · ${run.contract_profile_id || "未知契约"} · SHA ${(run.export_sha256 || "未记录").slice(0, 12)}`;
    const metrics = document.createElement("span"); metrics.className = "run-metrics"; metrics.textContent = metricSummary(run);
    if (run.error_message) metrics.textContent = run.error_message;
    info.append(title, progress, trace, metrics);
    if (run.external_result_sha256) {
      const resultTrace = document.createElement("span");
      resultTrace.textContent = `外部结果 ${String(run.external_result_format || "json").toUpperCase()} · Schema v${run.external_result_schema_version || 1} · SHA ${run.external_result_sha256.slice(0, 12)}${run.correction_of_run_id ? ` · 修订自 ${run.correction_of_run_id}` : ""}`;
      info.append(resultTrace);
    }
    const actions = document.createElement("div"); actions.className = "stack-form";
    const status = document.createElement("span"); status.className = `run-status ${run.status === 2 ? "success" : run.status === 3 ? "failed" : "running"}`;
    status.textContent = run.adapter_id === "manual-export" && run.status === 0 ? "等待外部结果" : evaluationStatus(run.status); actions.append(status);
    actions.append(makeButton("查看详情", "toggle-evaluation-detail", run.id));
    if ((run.status === 0 || run.status === 1) && run.adapter_id !== "manual-export") actions.append(makeButton("更新状态", "poll-evaluation", run.id));
    row.append(info, actions, buildEvaluationRunDetail(run)); container.append(row);
  }
  renderExternalResultOptions();
  renderReportOptions();
  renderComparisonOptions();
}

function renderExternalResultOptions() {
  const select = byId("external-result-run");
  const previous = select.value;
  const manualRuns = state.evaluationRuns.filter((run) => run.adapter_id === "manual-export");
  select.replaceChildren();
  const placeholder = document.createElement("option"); placeholder.value = "";
  placeholder.textContent = manualRuns.length ? "请选择手工评测记录" : "暂无手工评测记录";
  select.append(placeholder);
  for (const run of manualRuns) {
    const option = document.createElement("option"); option.value = run.id;
    const pending = run.status === 0 && run.result_source === "external_pending";
    option.textContent = `${formatDate(run.created_at)} · dataset ${run.dataset_id} · ${pending ? "等待首次结果" : "已完成，可创建修订"}`;
    select.append(option);
  }
  if (manualRuns.some((run) => run.id === previous)) select.value = previous;
  updateExternalResultVisibility();
}

function updateExternalResultVisibility() {
  const panel = byId("external-result-panel");
  const hint = byId("external-result-mode-hint");
  if (!panel || !hint) return;
  const manual = currentAdapterID() === "manual-export";
  const hasManualRuns = state.evaluationRuns.some((run) => run.adapter_id === "manual-export");
  panel.hidden = !manual;
  hint.hidden = manual || !hasManualRuns;
}

function clearExternalResultPreview(message = "尚未校验结果") {
  state.externalResultPreview = null;
  const preview = byId("external-result-preview");
  preview.className = "result-preview muted";
  preview.textContent = message;
  byId("save-external-result-button").disabled = true;
  byId("save-external-result-button").textContent = "确认保存结果";
}

function updateExternalResultFormatUI() {
  const format = byId("external-result-format").value;
  byId("external-result-content-label").textContent = format === "csv" ? "真实逐题结果 CSV" : "真实结果 JSON";
  clearExternalResultPreview("格式已切换，请粘贴并重新校验结果");
}

function parseExternalResult() {
  const runID = byId("external-result-run").value;
  const raw = byId("external-result-json").value.trim();
  const format = byId("external-result-format").value;
  if (!runID) throw new Error("请先选择手工评测记录");
  if (!raw) throw new Error(`请粘贴外部评测系统生成的真实结果 ${format.toUpperCase()}`);
  return { runID, raw, format };
}

async function previewExternalResult() {
  clearExternalResultPreview();
  try {
    const result = parseExternalResult();
    const response = await api(`/api/datasets/${encodeURIComponent(state.project.id)}/evaluations/${encodeURIComponent(result.runID)}/external-result/preview`, {
      method: "POST", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ format: result.format, content: result.raw }),
    });
    result.preview = response.data;
    state.externalResultPreview = result;
    const preview = byId("external-result-preview");
    if (response.data.valid) {
      preview.className = "result-preview valid";
      const missing = response.data.missing_question_ids?.length || 0;
      preview.textContent = `校验通过：格式 ${response.data.format.toUpperCase()} · Schema v${response.data.schema_version} · 逐题 ${response.data.item_count} 条 · 匹配 ${response.data.matched_count} 条 · 缺失 ${missing} 条 · SHA ${response.data.sha256.slice(0, 12)}${response.data.creates_revision ? " · 保存时将创建修订记录" : ""}。`;
      byId("save-external-result-button").disabled = false;
      byId("save-external-result-button").textContent = response.data.creates_revision ? "创建修订记录" : "确认保存结果";
    } else {
      preview.className = "result-preview invalid";
      preview.textContent = `校验未通过：${(response.data.issues || []).join("；")}`;
    }
  } catch (error) {
    const preview = byId("external-result-preview");
    preview.className = "result-preview invalid";
    preview.textContent = error.message;
  }
}

function reportEligibleRuns() {
  return state.evaluationRuns.filter((run) => run.status === 2 || run.status === 3);
}

function renderReportOptions() {
  const select = byId("report-run");
  const previous = select.value;
  const runs = reportEligibleRuns();
  select.replaceChildren();
  const placeholder = document.createElement("option"); placeholder.value = ""; placeholder.textContent = runs.length ? "请选择评测记录" : "暂无已完成记录"; select.append(placeholder);
  for (const run of runs) {
    const option = document.createElement("option"); option.value = run.id;
    option.textContent = `${formatDate(run.created_at)} · ${run.adapter_id === "manual-export" ? "手工结果" : run.knowledge_base_id || run.adapter_id}`;
    select.append(option);
  }
  if (runs.some((run) => run.id === previous)) select.value = previous;
}

function renderComparisonOptions() {
  const completed = reportEligibleRuns();
  for (const id of ["comparison-base", "comparison-target"]) {
    const select = byId(id); const previous = select.value; select.replaceChildren();
    const placeholder = document.createElement("option"); placeholder.value = ""; placeholder.textContent = "请选择"; select.append(placeholder);
    for (const run of completed) {
      const option = document.createElement("option"); option.value = run.id;
      option.textContent = `${formatDate(run.created_at)} · ${run.adapter_id === "manual-export" ? "手工结果" : run.knowledge_base_id || run.adapter_id}`; select.append(option);
    }
    if (completed.some((run) => run.id === previous)) select.value = previous;
  }
}

async function renderEvaluationComparison() {
  const base = byId("comparison-base").value;
  const target = byId("comparison-target").value;
  const container = byId("comparison-result"); container.replaceChildren();
  if (!base || !target || base === target) {
    container.className = "comparison-result muted";
    container.textContent = "请选择两个不同的已完成任务";
    return;
  }
  container.className = "comparison-result muted"; container.textContent = "正在生成逐题对比…";
  const response = await api(`/api/datasets/${encodeURIComponent(state.project.id)}/evaluation-comparison?base=${encodeURIComponent(base)}&target=${encodeURIComponent(target)}`);
  const comparison = response.data;
  container.replaceChildren();
  const summary = document.createElement("p");
  summary.textContent = `改善 ${comparison.summary.improved} · 退化 ${comparison.summary.regressed} · 不变 ${comparison.summary.unchanged} · 新增缺失 ${comparison.summary.newly_missing} · 不可比 ${comparison.summary.incomparable}`;
  const table = document.createElement("table"); table.className = "metric-table";
  const head = document.createElement("thead");
  const headRow = document.createElement("tr");
  for (const text of ["问题", "基准 Recall", "对比 Recall", "结论"]) { const th = document.createElement("th"); th.textContent = text; headRow.append(th); }
  head.append(headRow); table.append(head);
  const body = document.createElement("tbody");
  const labels = { improved: "改善", regressed: "退化", unchanged: "不变", newly_missing: "新增缺失", incomparable: "不可比" };
  for (const item of comparison.items) {
    const row = document.createElement("tr");
    const values = [`${item.question_id} · ${item.question}`, item.base_recall == null ? "—" : Number(item.base_recall).toFixed(4), item.target_recall == null ? "—" : Number(item.target_recall).toFixed(4), labels[item.state] || item.state];
    for (const [index, text] of values.entries()) {
      const cell = document.createElement("td"); cell.textContent = text;
      if (index === 3 && item.state !== "unchanged") cell.className = item.state === "improved" ? "positive" : item.state === "regressed" || item.state === "newly_missing" ? "negative" : "";
      row.append(cell);
    }
    body.append(row);
  }
  table.append(body); container.className = "comparison-result"; container.append(summary, table);
  setCollapsibleOutput("comparison-result", "toggle-comparison-button", false, "对比");
}

function setCollapsibleOutput(outputID, buttonID, collapsed, label) {
  const output = byId(outputID);
  const button = byId(buttonID);
  output.hidden = collapsed;
  button.hidden = false;
  button.setAttribute("aria-expanded", String(!collapsed));
  button.textContent = collapsed ? `展开${label}` : `收起${label}`;
}

function toggleCollapsibleOutput(outputID, buttonID, label) {
  setCollapsibleOutput(outputID, buttonID, !byId(outputID).hidden, label);
}

function reportPassageText(items) {
  return (items || []).map((item) => `[${item.id}] ${item.known ? item.text : "未知片段"}`).join("\n") || "—";
}

function renderEvaluationReportItems() {
  const report = state.evaluationReport;
  if (!report) return;
  const filter = byId("report-filter").value;
  const items = report.items.filter((item) => filter === "all"
    || (filter === "zero" && item.metric_computable && item.recall === 0)
    || (filter === "low" && item.metric_computable && item.recall < 1)
    || (filter === "failed" && item.state === "failed")
    || (filter === "missing" && item.state === "missing")
    || (filter === "incorrect" && ["incorrect", "partial"].includes(item.answer_judgement))
    || (filter === "unreviewed" && !item.answer_judgement));
  const body = byId("report-items"); body.replaceChildren();
  for (const item of items) {
    const row = document.createElement("tr");
    const selectable = item.state !== "completed" || item.recall < 1 || ["incorrect", "partial"].includes(item.answer_judgement);
    const selection = document.createElement("td");
    const checkbox = document.createElement("input"); checkbox.type = "checkbox"; checkbox.className = "feedback-question"; checkbox.value = String(item.question_id); checkbox.disabled = !selectable; checkbox.checked = state.feedbackQuestionIDs.has(item.question_id);
    checkbox.setAttribute("aria-label", `选择问题 ${item.question_id}`); selection.append(checkbox);
    const status = document.createElement("td"); status.textContent = `${item.question_id}\n${item.state}`;
    const qa = document.createElement("td"); qa.textContent = `${item.question}\n标准：${item.standard_answer}\n实际：${item.actual_answer || "—"}`;
    const assessment = document.createElement("td");
    const judgementLabels = { correct: "正确", partial: "部分正确", incorrect: "错误" };
    assessment.textContent = `${judgementLabels[item.answer_judgement] || "未评价"}${item.answer_score == null ? "" : ` · ${Number(item.answer_score).toFixed(4)} 分`}\n${item.assessment_source || "—"}\n${item.review_comment || "—"}`;
    const metrics = document.createElement("td"); metrics.textContent = item.metric_computable ? `P ${item.precision.toFixed(4)}\nR ${item.recall.toFixed(4)}\nHit ${item.hit.toFixed(0)}\nRR ${item.reciprocal_rank.toFixed(4)}` : "不可计算";
    const passages = document.createElement("td"); passages.textContent = `黄金：\n${reportPassageText(item.gold_passages)}\n实际：\n${reportPassageText(item.retrieved_passages)}`;
    const error = document.createElement("td"); error.textContent = `${item.error_message || "—"}\n${item.latency_ms || 0} ms`;
    row.append(selection, status, qa, assessment, metrics, passages, error); body.append(row);
  }
  byId("report-table").hidden = false;
}

function renderEvaluationReport(report) {
  state.evaluationReport = report;
  state.feedbackQuestionIDs = new Set();
  const summary = byId("report-summary"); summary.className = "report-summary"; summary.replaceChildren();
  const coverage = document.createElement("p"); coverage.textContent = `总数 ${report.summary.total} · 已运行 ${report.summary.executed} · 失败 ${report.summary.failed} · 缺失 ${report.summary.missing} · 不可计算 ${report.summary.uncomputable} · 零召回 ${report.summary.zero_recall}`;
  const metrics = document.createElement("p"); metrics.textContent = `本地宏平均（可计算 ${report.metrics.computable}）：Precision ${report.metrics.precision.toFixed(4)} · Recall ${report.metrics.recall.toFixed(4)} · Hit ${report.metrics.hit.toFixed(4)} · MRR ${report.metrics.mrr.toFixed(4)}`;
  const answer = report.answer_assessment;
  const assessments = document.createElement("p"); assessments.textContent = `显式答案评价：已评价 ${answer.reviewed} · 正确 ${answer.correct} · 部分正确 ${answer.partial} · 错误 ${answer.incorrect} · 未评价 ${answer.unreviewed} · 平均分 ${answer.average_score.toFixed(4)}（${answer.scored} 条）`;
  const note = document.createElement("p"); note.className = "muted-text"; note.textContent = report.answer_assessment_note;
  summary.append(coverage, metrics, assessments, note);
  for (const warning of report.warnings || []) { const item = document.createElement("p"); item.className = "report-warning"; item.textContent = warning; summary.append(item); }
  const groupContainer = byId("report-groups"); groupContainer.replaceChildren();
  const groupTable = document.createElement("table"); const groupBody = document.createElement("tbody");
  const groupHead = document.createElement("thead"); const groupHeadRow = document.createElement("tr");
  for (const text of ["维度", "值", "样本", "可计算", "Precision", "Recall", "Hit", "MRR"]) { const th = document.createElement("th"); th.textContent = text; groupHeadRow.append(th); }
  groupHead.append(groupHeadRow); groupTable.append(groupHead);
  for (const group of report.groups || []) { const row = document.createElement("tr"); for (const value of [group.dimension, group.value, group.count, group.metrics.computable, group.metrics.precision.toFixed(4), group.metrics.recall.toFixed(4), group.metrics.hit.toFixed(4), group.metrics.mrr.toFixed(4)]) { const td = document.createElement("td"); td.textContent = String(value); row.append(td); } groupBody.append(row); }
  groupTable.append(groupBody); groupContainer.append(groupTable); byId("report-groups-panel").hidden = false;
  const root = `/api/datasets/${encodeURIComponent(state.project.id)}/evaluations/${encodeURIComponent(report.run_id)}`;
  byId("report-json-link").href = `${root}/report?download=1`;
  byId("report-html-link").href = `${root}/report.html`;
  byId("report-csv-link").href = `${root}/failures.csv`;
  byId("report-downloads").hidden = false;
  byId("feedback-id").value = `${state.project.id}-feedback-${report.run_id.slice(0, 8).toLowerCase()}`;
  byId("feedback-name").value = `${state.project.name} · 失败样本回流`;
  byId("feedback-version").value = state.project.version;
  byId("feedback-form").hidden = false;
  setCollapsibleOutput("report-output", "toggle-report-button", false, "报告");
  renderEvaluationReportItems();
}

async function loadEvaluationReport() {
  const runID = byId("report-run").value;
  if (!runID) throw new Error("请选择一条已完成的评测记录");
  const response = await api(`/api/datasets/${encodeURIComponent(state.project.id)}/evaluations/${encodeURIComponent(runID)}/report`);
  renderEvaluationReport(response.data);
}

async function pollEvaluation(runID) {
  const apiKey = byId("evaluation-api-key").value.trim();
  if (!apiKey) throw new Error("请输入空间 API Key 后再更新任务状态");
  await api(`/api/datasets/${encodeURIComponent(state.project.id)}/evaluations/${encodeURIComponent(runID)}/poll`, {
    method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ api_key: apiKey }),
  });
}

async function importCSV(kind, file) {
  if (!state.project || !file) return;
  if (state.dirty) await saveProject();
  const result = await api(`/api/datasets/${encodeURIComponent(state.project.id)}/import/csv`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ kind, csv: await file.text() }),
  });
  state.project = result.data.project;
  resetPassageForm();
  resetQuestionForm();
  setDirty(false);
  renderAll();
  await loadDatasetList();
  showToast(`已导入 ${result.data.summary.imported_count} 条${kind === "passages" ? "语料" : "问题"}`);
}

async function exportDataset() {
  if (!state.project) return;
  if (state.dirty) await saveProject();
  const response = await fetch(`/api/datasets/${encodeURIComponent(state.project.id)}/export/weknora`, { method: "POST" });
  if (!response.ok) {
    const body = await response.json();
    if (body.error?.report) renderValidation(body.error.report);
    throw new Error(body.error?.message || "导出失败");
  }
  const blob = await response.blob();
  const disposition = response.headers.get("content-disposition") || "";
  const match = disposition.match(/filename="?([^";]+)"?/i);
  const fileName = match?.[1] || `${state.project.id}-weknora.zip`;
  const link = document.createElement("a");
  link.href = URL.createObjectURL(blob); link.download = fileName; link.click();
  setTimeout(() => URL.revokeObjectURL(link.href), 1000);
  showToast(`已导出 ${fileName}`);
  await loadHistory();
}

byId("create-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  try {
    const result = await api("/api/datasets", {
      method: "POST", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ id: byId("create-id").value, name: byId("create-name").value, version: byId("create-version").value }),
    });
    event.target.reset(); byId("create-version").value = "0.1.0";
    await loadDatasetList(); await openDataset(result.data.id, true);
  } catch (error) { showToast(error.message, true); }
});

datasetList.addEventListener("click", (event) => {
  const button = event.target.closest("button[data-id]");
  if (button) openDataset(button.dataset.id).catch((error) => showToast(error.message, true));
});

byId("meta-form").addEventListener("input", () => setDirty());
byId("save-button").addEventListener("click", () => saveProject().catch((error) => showToast(error.message, true)));

byId("passage-form").addEventListener("submit", (event) => {
  event.preventDefault();
  const existing = state.project.passages.find((item) => item.id === state.editingPassageID);
  const passage = existing || { id: state.project.next_passage_id++ };
  passage.text = byId("passage-text").value;
  passage.source = byId("passage-source").value;
  passage.tags = parseTags(byId("passage-tags").value);
  passage.review_state = byId("passage-review-state").value;
  if (!existing) state.project.passages.push(passage);
  resetPassageForm(); setDirty(); renderAll();
});
byId("passage-cancel").addEventListener("click", resetPassageForm);
byId("passage-search").addEventListener("input", renderPassages);
byId("passage-table").addEventListener("click", (event) => {
  const button = event.target.closest("button[data-action]"); if (!button) return;
  const id = Number(button.dataset.id);
  if (button.dataset.action === "edit-passage") editPassage(id);
  if (button.dataset.action === "delete-passage") deletePassage(id);
});

byId("source-profile-select").addEventListener("change", () => resetSourceImport());
byId("load-source-kbs").addEventListener("click", () => loadSourceKnowledgeBases().catch((error) => showToast(error.message, true)));
byId("source-kb-select").addEventListener("change", () => {
  state.sourceImport.documents = []; state.sourceImport.chunks = []; state.sourceImport.selectedChunkIDs.clear(); state.sourceImport.preview = null;
  replaceSimpleOptions(byId("source-document-select"), [], "请先读取文档", (item) => item.title);
  byId("source-document-select").disabled = true;
  byId("load-source-documents").disabled = !byId("source-kb-select").value;
  byId("load-source-chunks").disabled = true; byId("source-chunk-browser").hidden = true;
});
byId("load-source-documents").addEventListener("click", () => loadSourceDocuments().catch((error) => showToast(error.message, true)));
byId("source-document-select").addEventListener("change", () => {
  state.sourceImport.chunks = []; state.sourceImport.selectedChunkIDs.clear(); state.sourceImport.preview = null;
  byId("load-source-chunks").disabled = !byId("source-document-select").value;
  byId("source-chunk-browser").hidden = true;
});
byId("load-source-chunks").addEventListener("click", () => loadSourceChunks(1).catch((error) => showToast(error.message, true)));
byId("source-chunk-table-body").addEventListener("change", (event) => {
  const checkbox = event.target.closest('input[type="checkbox"]'); if (!checkbox) return;
  if (checkbox.checked) state.sourceImport.selectedChunkIDs.add(checkbox.value); else state.sourceImport.selectedChunkIDs.delete(checkbox.value);
  state.sourceImport.preview = null; renderSourceChunks(); renderSourceImportPreview();
});
byId("select-source-page").addEventListener("click", () => {
  for (const chunk of state.sourceImport.chunks) if (sourceChunkSelectable(chunk)) state.sourceImport.selectedChunkIDs.add(chunk.id);
  state.sourceImport.preview = null; renderSourceChunks(); renderSourceImportPreview();
});
byId("clear-source-selection").addEventListener("click", () => {
  state.sourceImport.selectedChunkIDs.clear(); state.sourceImport.preview = null; renderSourceChunks(); renderSourceImportPreview();
});
byId("source-prev-page").addEventListener("click", () => loadSourceChunks(state.sourceImport.chunkPage - 1).catch((error) => showToast(error.message, true)));
byId("source-next-page").addEventListener("click", () => loadSourceChunks(state.sourceImport.chunkPage + 1).catch((error) => showToast(error.message, true)));
byId("source-changed-policy").addEventListener("change", () => { state.sourceImport.preview = null; renderSourceImportPreview(); });
byId("preview-source-import").addEventListener("click", () => previewSourceImport().catch((error) => showToast(error.message, true)));
byId("confirm-source-import").addEventListener("click", () => confirmSourceImport().catch((error) => showToast(error.message, true)));
byId("go-to-generation-after-import").addEventListener("click", () => document.querySelector('.tab[data-tab="generation"]').click());

byId("question-form").addEventListener("submit", (event) => {
  event.preventDefault();
  let retrievalFilters;
  try { retrievalFilters = parseKeyValueLines(byId("question-retrieval-filters").value); }
  catch (error) { showToast(error.message, true); return; }
  const existing = state.project.questions.find((item) => item.id === state.editingQuestionID);
  const question = existing || { id: state.project.next_question_id++ };
  question.text = byId("question-text").value;
  question.answer = byId("question-answer").value;
  question.category = byId("question-category").value;
  question.difficulty = byId("question-difficulty").value;
  question.tags = parseTags(byId("question-tags").value);
  question.review_state = byId("question-review-state").value;
  question.answer_key_points = parseLineList(byId("question-answer-key-points").value);
  const answerable = byId("question-answerable").value;
  if (answerable === "") delete question.answerable;
  else question.answerable = answerable === "true";
  question.expected_documents = parseLineList(byId("question-expected-documents").value);
  question.forbidden_documents = parseLineList(byId("question-forbidden-documents").value);
  question.test_role = byId("question-test-role").value;
  question.retrieval_filters = retrievalFilters;
  question.dataset_version = byId("question-dataset-version").value;
  question.annotation_source = byId("question-annotation-source").value;
  question.relevant_passage_ids = selectedPassageIDs();
  if (!existing) state.project.questions.push(question);
  resetQuestionForm(); setDirty(); renderAll();
});
byId("question-cancel").addEventListener("click", resetQuestionForm);
byId("question-search").addEventListener("input", renderQuestions);
byId("relation-search").addEventListener("input", () => renderPassageChoices());
byId("passage-choices").addEventListener("change", (event) => {
  const checkbox = event.target.closest('input[type="checkbox"]');
  if (!checkbox) return;
  const id = Number(checkbox.value);
  if (checkbox.checked) state.questionPassageSelection.add(id);
  else state.questionPassageSelection.delete(id);
});
byId("question-table").addEventListener("click", (event) => {
  const button = event.target.closest("button[data-action]"); if (!button) return;
  const id = Number(button.dataset.id);
  if (button.dataset.action === "edit-question") editQuestion(id);
  if (button.dataset.action === "delete-question") deleteQuestion(id);
});

byId("passage-csv-file").addEventListener("change", async (event) => {
  const file = event.target.files?.[0];
  try { await importCSV("passages", file); }
  catch (error) { showToast(error.message, true); }
  event.target.value = "";
});
byId("question-csv-file").addEventListener("change", async (event) => {
  const file = event.target.files?.[0];
  try { await importCSV("questions", file); }
  catch (error) { showToast(error.message, true); }
  event.target.value = "";
});
byId("passage-template-button").addEventListener("click", () => {
  downloadTextFile("passages-template.csv", "id,text,source,tags,review_state\n,设备离线时先检查电源和网络,故障手册,离线|网络,approved\n");
});
byId("question-template-button").addEventListener("click", () => {
  downloadTextFile("questions-template.csv", "id,text,answer,relevant_passage_ids,category,difficulty,tags,review_state,answer_key_points,answerable,expected_documents,forbidden_documents,test_role,retrieval_filters,dataset_version,annotation_source\n,设备离线怎么排查？,先检查电源和网络,1,故障排查,easy,离线|网络,approved,检查电源|检查网络,true,故障手册|网络指南,,普通用户,department=售后|product=网关,0.1.0,业务专家\n");
});

byId("generation-connection-select").addEventListener("change", (event) => {
  applyGenerationConnection(state.generationConnections.find((item) => item.id === event.target.value));
});
byId("new-generation-connection-button").addEventListener("click", resetGenerationConnectionForm);
byId("save-generation-connection-button").addEventListener("click", async () => {
  try {
    const profile = await saveGenerationConnection();
    showToast(`生成配置已保存：${profile.name}`);
  } catch (error) { showToast(error.message, true); }
});
byId("generation-api-key").addEventListener("change", async () => {
  if (!byId("generation-connection-select").value) return;
  try { await saveGenerationConnection(); showToast("生成 API Key 已自动保存"); }
  catch (error) { showToast(error.message, true); }
});
byId("test-generation-connection-button").addEventListener("click", async () => {
  const id = byId("generation-connection-select").value;
  if (!id) { showToast("请先保存生成配置", true); return; }
  const button = byId("test-generation-connection-button"); button.disabled = true;
  try {
    await api(`/api/generation-connections/${encodeURIComponent(id)}/test`, { method: "POST" });
    showToast("生成模型连接测试成功");
  } catch (error) { showToast(error.message, true); }
  finally { button.disabled = false; }
});
byId("delete-generation-connection-button").addEventListener("click", async () => {
  const id = byId("generation-connection-select").value;
  if (!id) { showToast("没有可删除的生成配置", true); return; }
  try {
    const refs = await configurationReferences("generation", id);
    if (refs.workspace_default || refs.dataset_ids.length) {
      throw new Error(`配置仍被引用${refs.workspace_default ? "（workspace 默认）" : ""}${refs.dataset_ids.length ? `：${refs.dataset_ids.join("、")}` : ""}，请先解除引用或选择替代配置`);
    }
    if (!confirm(`删除生成配置“${id}”？历史任务仍会保留。`)) return;
    await api(`/api/generation-connections/${encodeURIComponent(id)}`, { method: "DELETE" });
    resetGenerationConnectionForm(); await loadGenerationConnections(); showToast("生成配置已删除");
  } catch (error) { showToast(error.message, true); }
});
for (const id of ["generation-question-count", "generation-max-output", "generation-input-price", "generation-output-price", "generation-model"]) {
  byId(id).addEventListener("input", updateGenerationEstimate);
}
byId("generation-passage-choices").addEventListener("change", (event) => {
  const checkbox = event.target.closest('input[type="checkbox"]'); if (!checkbox) return;
  const id = Number(checkbox.value);
  if (checkbox.checked) state.generationPassageSelection.add(id); else state.generationPassageSelection.delete(id);
  updateGenerationEstimate();
});
byId("select-all-generation-passages").addEventListener("click", () => {
  state.generationPassageSelection = new Set((state.project?.passages || []).map((item) => item.id)); renderGenerationPassageChoices();
});
byId("clear-generation-passages").addEventListener("click", () => {
  state.generationPassageSelection = new Set(); renderGenerationPassageChoices();
});
byId("generation-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  const connectionID = byId("generation-connection-select").value;
  if (!connectionID) { showToast("请先保存并选择生成模型配置", true); return; }
  if (!state.generationPassageSelection.size) { showToast("请至少选择一条来源语料", true); return; }
  const estimate = estimateGenerationTokens();
  if (!confirm(`将基于 ${estimate.passages} 条语料生成约 ${estimate.candidates} 个候选。模型调用可能产生费用，确定继续吗？`)) return;
  const button = byId("start-generation-button"); button.disabled = true;
  try {
    if (state.dirty) await saveProject();
    const response = await api(`/api/datasets/${encodeURIComponent(state.project.id)}/generation-jobs`, {
      method: "POST", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        connection_id: connectionID, passage_ids: [...state.generationPassageSelection],
        questions_per_passage: Number(byId("generation-question-count").value),
        category: byId("generation-category").value.trim(), difficulty: byId("generation-difficulty").value,
        additional_guidance: byId("generation-guidance").value.trim(), regenerate: byId("generation-regenerate").checked,
      }),
    });
    await loadGenerationJobs(response.data.id); await loadGenerationJob(response.data.id);
    showToast("生成任务已创建，正在后台执行");
  } catch (error) { showToast(error.message, true); }
  finally { button.disabled = false; }
});
byId("refresh-generation-button").addEventListener("click", async () => {
  try {
    await loadGenerationConnections(); await loadGenerationJobs();
    if (byId("generation-job-select").value) await loadGenerationJob();
    showToast("生成配置与任务已刷新");
  } catch (error) { showToast(error.message, true); }
});
byId("load-generation-job-button").addEventListener("click", () => loadGenerationJob().catch((error) => showToast(error.message, true)));
byId("generation-job-select").addEventListener("change", (event) => {
  if (event.target.value) loadGenerationJob(event.target.value).catch((error) => showToast(error.message, true));
});
byId("cancel-generation-job-button").addEventListener("click", async () => {
  if (!state.project || !state.generationJob) return;
  try {
    const response = await api(`/api/datasets/${encodeURIComponent(state.project.id)}/generation-jobs/${encodeURIComponent(state.generationJob.id)}/cancel`, { method: "POST" });
    renderGenerationJob(response.data); showToast("已请求取消，当前已发送的请求完成后停止");
  } catch (error) { showToast(error.message, true); }
});
byId("select-pending-candidates-button").addEventListener("click", () => {
  byId("generation-candidates").querySelectorAll(".generation-candidate-select:not(:disabled)").forEach((item) => { item.checked = true; });
});
byId("approve-candidates-button").addEventListener("click", async () => {
  try { await reviewGenerationCandidates("approve"); showToast("所选候选已批准并写入正式问题列表"); }
  catch (error) { showToast(error.message, true); }
});
byId("reject-candidates-button").addEventListener("click", async () => {
  try { await reviewGenerationCandidates("reject"); showToast("所选候选已拒绝"); }
  catch (error) { showToast(error.message, true); }
});

byId("dataset-generation-profile-select").addEventListener("change", (event) => {
  if (!state.project) return;
  state.project.generation_profile_id = event.target.value;
  const profile = state.generationConnections.find((item) => item.id === event.target.value);
  if (profile) applyGenerationConnection(profile);
  else resetGenerationConnectionForm();
  setDirty(); renderDatasetProfileSummaries();
});
byId("dataset-evaluation-profile-select").addEventListener("change", (event) => {
  if (!state.project) return;
  state.project.evaluation_profile_id = event.target.value;
  const profile = state.connectionProfiles.find((item) => item.id === event.target.value);
  if (profile) applyConnectionProfile(profile);
  else resetConnectionForm();
  setDirty(); renderDatasetProfileSummaries();
});
byId("save-default-settings-button").addEventListener("click", async () => {
  try { await saveWorkspaceSettings(); showToast("新数据集默认配置已保存"); }
  catch (error) { showToast(error.message, true); }
});
byId("show-datasets-button").addEventListener("click", () => {
  if (state.project) applyDatasetProfileBindings();
  showWorkspace("datasets");
});
byId("back-to-datasets-button").addEventListener("click", () => {
  if (state.project) applyDatasetProfileBindings();
  showWorkspace("datasets");
});
byId("show-config-button").addEventListener("click", () => showWorkspace("configuration"));
document.querySelectorAll(".open-config-center").forEach((button) => button.addEventListener("click", () => showWorkspace("configuration", button.dataset.configSection)));

document.querySelectorAll(".tab").forEach((button) => button.addEventListener("click", () => {
  document.querySelectorAll(".tab").forEach((item) => item.classList.toggle("active", item === button));
  document.querySelectorAll(".tab-panel").forEach((panel) => panel.classList.toggle("active", panel.id === `tab-${button.dataset.tab}`));
}));

byId("validate-button").addEventListener("click", () => runValidation().catch((error) => showToast(error.message, true)));
byId("export-button").addEventListener("click", () => exportDataset().catch((error) => showToast(error.message, true)));

byId("create-snapshot-button").addEventListener("click", async () => {
  if (!state.project) return;
  try {
    if (state.dirty) await saveProject();
    await api(`/api/datasets/${encodeURIComponent(state.project.id)}/snapshots`, { method: "POST" });
    await loadHistory();
    showToast("已创建版本快照");
  } catch (error) { showToast(error.message, true); }
});
byId("refresh-history-button").addEventListener("click", () => loadHistory().catch((error) => showToast(error.message, true)));
byId("export-history-list").addEventListener("click", async (event) => {
  const button = event.target.closest("button[data-action]");
  if (!button || !state.project) return;
  const deployed = button.dataset.action === "mark-export-deployed";
  const profileID = byId("connection-profile-select").value;
  if (deployed && !profileID) { showToast("请先保存并选择目标环境配置", true); return; }
  try {
    await api(`/api/datasets/${encodeURIComponent(state.project.id)}/exports/${encodeURIComponent(button.dataset.id)}/deployment`, {
      method: "PUT", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        connection_profile_id: profileID,
        dataset_id: byId("connection-dataset-id").value.trim(),
        status: deployed ? "deployed" : "not_deployed",
      }),
    });
    await loadHistory();
    showToast(deployed ? "已记录数据包部署信息" : "已标记为未部署");
  } catch (error) { showToast(error.message, true); }
});
byId("snapshot-list").addEventListener("click", async (event) => {
  const button = event.target.closest('button[data-action="restore-snapshot"]');
  if (!button || !state.project) return;
  if (!confirm("恢复快照会覆盖当前项目内容，现有项目会先保留最近备份。确定继续吗？")) return;
  try {
    await api(`/api/datasets/${encodeURIComponent(state.project.id)}/snapshots/${encodeURIComponent(button.dataset.id)}/restore`, { method: "POST" });
    await openDataset(state.project.id, true);
    showToast("快照已恢复");
  } catch (error) { showToast(error.message, true); }
});

byId("connection-profile-select").addEventListener("change", (event) => {
  const profile = state.connectionProfiles.find((item) => item.id === event.target.value);
  applyConnectionProfile(profile);
});
byId("evaluation-api-key").addEventListener("change", async () => {
  if (!byId("connection-profile-select").value || currentAdapterID() === "manual-export") return;
  try {
    const profile = await saveConnectionProfile();
    showToast(`API Key 已自动保存到环境配置：${profile.name}`);
  } catch (error) { showToast(error.message, true); }
});
byId("new-connection-button").addEventListener("click", resetConnectionForm);
byId("save-connection-button").addEventListener("click", async () => {
  try {
    const profile = await saveConnectionProfile();
    showToast(`环境配置已保存：${profile.name}`);
  } catch (error) { showToast(error.message, true); }
});
byId("delete-connection-button").addEventListener("click", async () => {
  const id = byId("connection-profile-select").value;
  if (!id) { showToast("没有可删除的已保存配置", true); return; }
  try {
    const refs = await configurationReferences("evaluation", id);
    if (refs.workspace_default || refs.dataset_ids.length) {
      throw new Error(`配置仍被引用${refs.workspace_default ? "（workspace 默认）" : ""}${refs.dataset_ids.length ? `：${refs.dataset_ids.join("、")}` : ""}，请先解除引用或选择替代配置`);
    }
    if (!confirm(`删除环境配置“${id}”？不会删除数据集或评测记录。`)) return;
    await api(`/api/connections/${encodeURIComponent(id)}`, { method: "DELETE" });
    resetConnectionForm(); await loadConnectionProfiles(); showToast("环境配置已删除");
  } catch (error) { showToast(error.message, true); }
});
byId("check-compatibility-button").addEventListener("click", async () => {
  const button = byId("check-compatibility-button"); button.disabled = true;
  try {
    const report = await checkCompatibility();
    showToast(`只读检查完成：通过 ${report.summary.passed} 项`);
  } catch (error) { showToast(error.message, true); }
  finally { button.disabled = false; }
});
byId("download-compatibility-button").addEventListener("click", () => {
  if (!state.compatibilityReport) return;
  downloadTextFile(
    `${state.compatibilityReport.environment}-${state.compatibilityReport.adapter_id}-compatibility-report.json`,
    JSON.stringify(state.compatibilityReport, null, 2),
    "application/json;charset=utf-8",
  );
});
byId("connection-adapter").addEventListener("change", () => {
  updateConnectionAdapterUI();
});
byId("connection-environment").addEventListener("change", () => {
  const creating = !byId("connection-profile-select").value;
  if (creating && byId("connection-environment").value === "production") {
    byId("connection-adapter").value = "manual-export";
  }
  updateConnectionAdapterUI();
});

byId("load-evaluation-resources-button").addEventListener("click", async () => {
  const button = byId("load-evaluation-resources-button");
  button.disabled = true;
  const originalText = button.textContent;
  button.textContent = "读取中…";
  try {
    const counts = await loadEvaluationResources();
    showToast(`WeKnora 资源已更新：${counts}`);
  } catch (error) {
    showToast(error.message, true);
  } finally {
    button.disabled = false;
    button.textContent = originalText;
  }
});

byId("evaluation-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  if (!state.project) return;
  const manual = currentAdapterID() === "manual-export";
  const productionRemote = byId("connection-environment").value === "production" && !manual;
  const confirmation = manual
    ? "创建手工评测记录不会访问远程接口。确认所选数据包已经部署，并继续创建记录吗？"
    : productionRemote
      ? "生产自动对接当前为实验性。继续会把 API Key 和评测请求发送到所填生产地址、调用模型并可能产生费用。确定继续吗？"
      : "启动评测会请求本地 WeKnora 并调用所选模型，可能产生费用。确定继续吗？";
  if (!confirm(confirmation)) return;
  try {
    const remote = !manual;
    const payload = {
      adapter_id: currentAdapterID(),
      connection_profile_id: byId("connection-profile-select").value,
      base_url: remote ? byId("evaluation-base-url").value : "",
      api_key: remote ? byId("evaluation-api-key").value : "",
      dataset_id: byId("connection-dataset-id").value,
      export_id: byId("evaluation-export").value,
      knowledge_base_id: remote ? byId("evaluation-kb-id").value : "",
      chat_id: remote ? byId("evaluation-chat-id").value : "",
      rerank_id: remote ? byId("evaluation-rerank-id").value : "",
      dataset_deployed: byId("evaluation-deployed").checked,
    };
    const result = await api(`/api/datasets/${encodeURIComponent(state.project.id)}/evaluations`, {
      method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(payload),
    });
    await loadHistory();
    if (manual) {
      byId("external-result-run").value = result.data.id;
      byId("external-result-json").value = "";
      clearExternalResultPreview("手工记录已创建并选中。请在外部系统完成评测后粘贴真实结果 JSON。 ");
    }
    showToast(manual ? `手工评测记录已创建：${result.data.task_id}` : `评测任务已启动：${result.data.task_id}`);
  } catch (error) {
    if (error.payload?.error?.report) renderValidation(error.payload.error.report);
    showToast(error.message, true);
  }
});

byId("validate-external-result-button").addEventListener("click", previewExternalResult);
byId("external-result-run").addEventListener("change", () => clearExternalResultPreview("记录已切换，请重新校验结果"));
byId("external-result-format").addEventListener("change", updateExternalResultFormatUI);
byId("external-result-json").addEventListener("input", () => clearExternalResultPreview("结果已修改，请重新校验"));

byId("external-result-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  if (!state.project) return;
  const runID = byId("external-result-run").value;
  if (!runID) { showToast("请选择手工评测记录", true); return; }
  const raw = byId("external-result-json").value.trim();
  const format = byId("external-result-format").value;
  if (!state.externalResultPreview || state.externalResultPreview.runID !== runID || state.externalResultPreview.raw !== raw || state.externalResultPreview.format !== format || !state.externalResultPreview.preview?.valid) {
    showToast("请先校验当前记录和结果内容", true);
    return;
  }
  const revision = state.externalResultPreview.preview.creates_revision;
  const confirmation = revision
    ? "所选记录已经完成。确认创建一条新的修订记录？原记录不会被覆盖。"
    : "确认把预览中的外部结果保存到这条手工评测记录？保存后该记录将标记为完成。";
  if (!confirm(confirmation)) return;
  try {
    const result = await api(`/api/datasets/${encodeURIComponent(state.project.id)}/evaluations/${encodeURIComponent(runID)}/external-result/import`, {
      method: "POST", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ format, content: raw }),
    });
    byId("external-result-json").value = "";
    clearExternalResultPreview(revision ? `修订记录已创建：${result.data.id}` : "结果已保存。可继续选择记录导入修订版本。 ");
    await loadHistory();
    showToast(revision ? "外部评测修订记录已创建" : "外部评测结果已导入");
  } catch (error) {
    showToast(error.message, true);
  }
});

byId("evaluation-run-list").addEventListener("click", async (event) => {
  const detailButton = event.target.closest('button[data-action="toggle-evaluation-detail"]');
  if (detailButton) {
    const row = detailButton.closest(".evaluation-run");
    const detail = row?.querySelector(".evaluation-run-detail");
    if (!detail) return;
    detail.hidden = !detail.hidden;
    detailButton.textContent = detail.hidden ? "查看详情" : "收起详情";
    detailButton.setAttribute("aria-expanded", String(!detail.hidden));
    if (!detail.hidden) await loadEvaluationRunQuestions(detailButton.dataset.id, detail);
    return;
  }
  const button = event.target.closest('button[data-action="poll-evaluation"]');
  if (!button) return;
  try {
    await pollEvaluation(button.dataset.id);
    await loadHistory();
    showToast("评测状态已更新");
  } catch (error) { showToast(error.message, true); }
});

byId("poll-evaluations-button").addEventListener("click", async () => {
  const active = state.evaluationRuns.filter((run) => (run.status === 0 || run.status === 1) && run.adapter_id !== "manual-export");
  if (!active.length) { showToast("没有进行中的评测任务"); return; }
  try {
    for (const run of active) await pollEvaluation(run.id);
    await loadHistory();
    showToast(`已更新 ${active.length} 个任务`);
  } catch (error) { showToast(error.message, true); }
});

byId("load-report-button").addEventListener("click", async () => {
  try { await loadEvaluationReport(); } catch (error) { showToast(error.message, true); }
});
byId("toggle-report-button").addEventListener("click", () => {
  toggleCollapsibleOutput("report-output", "toggle-report-button", "报告");
});
byId("report-filter").addEventListener("change", renderEvaluationReportItems);
byId("report-items").addEventListener("change", (event) => {
  const checkbox = event.target.closest("input.feedback-question");
  if (!checkbox) return;
  const questionID = Number(checkbox.value);
  if (checkbox.checked) state.feedbackQuestionIDs.add(questionID);
  else state.feedbackQuestionIDs.delete(questionID);
});
byId("report-run").addEventListener("change", () => {
  state.evaluationReport = null;
  state.feedbackQuestionIDs = new Set();
  byId("report-downloads").hidden = true;
  byId("report-groups-panel").hidden = true;
  byId("report-table").hidden = true;
  byId("feedback-form").hidden = true;
  byId("report-output").hidden = false;
  byId("toggle-report-button").hidden = true;
  byId("report-summary").className = "report-summary muted";
  byId("report-summary").textContent = "记录已切换，请重新生成报告";
});
byId("feedback-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  if (!state.evaluationReport) return;
  const questionIDs = [...state.feedbackQuestionIDs].sort((a, b) => a - b);
  if (!questionIDs.length) { showToast("请先勾选要回流的失败问题", true); return; }
  try {
    const response = await api(`/api/datasets/${encodeURIComponent(state.project.id)}/evaluations/${encodeURIComponent(state.evaluationReport.run_id)}/feedback-dataset`, {
      method: "POST", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ id: byId("feedback-id").value.trim(), name: byId("feedback-name").value.trim(), version: byId("feedback-version").value.trim(), question_ids: questionIDs }),
    });
    await loadDatasetList();
    await openDataset(response.data.id, true);
    showToast(`已创建回流数据集，共 ${questionIDs.length} 个问题`);
  } catch (error) { showToast(error.message, true); }
});
byId("compare-evaluations-button").addEventListener("click", async () => {
  try { await renderEvaluationComparison(); } catch (error) { showToast(error.message, true); }
});
byId("toggle-comparison-button").addEventListener("click", () => {
  toggleCollapsibleOutput("comparison-result", "toggle-comparison-button", "对比");
});

byId("delete-dataset-button").addEventListener("click", async () => {
  if (!state.project || !confirm(`将数据集“${state.project.name}”移入本地回收站？`)) return;
  try {
    await api(`/api/datasets/${encodeURIComponent(state.project.id)}`, { method: "DELETE" });
    state.project = null; setDirty(false); editor.hidden = true; emptyState.hidden = false;
    await loadDatasetList(); showToast("数据集已移入回收站");
  } catch (error) { showToast(error.message, true); }
});

byId("copy-button").addEventListener("click", async () => {
  if (!state.project) return;
  const id = prompt("新数据集 ID（小写字母、数字、短横线）", `${state.project.id}-copy`);
  if (!id) return;
  const name = prompt("新数据集名称", `${state.project.name} 副本`);
  if (!name) return;
  try {
    await api("/api/datasets", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ id, name, version: state.project.version }) });
    const clone = structuredClone(state.project);
    clone.id = id; clone.name = name;
    await api(`/api/datasets/${encodeURIComponent(id)}`, { method: "PUT", headers: { "Content-Type": "application/json" }, body: JSON.stringify(clone) });
    await loadDatasetList(); await openDataset(id, true); showToast("数据集已复制");
  } catch (error) { showToast(error.message, true); }
});

byId("import-file").addEventListener("change", async (event) => {
  const file = event.target.files?.[0]; if (!file) return;
  try {
    const project = JSON.parse(await file.text());
    await api("/api/import/project", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(project) });
    await loadDatasetList(); await openDataset(project.id, true); showToast("项目已导入");
  } catch (error) { showToast(error.message, true); }
  event.target.value = "";
});

byId("weknora-import-file").addEventListener("change", async (event) => {
  const file = event.target.files?.[0];
  if (!file) return;
  try {
    if (file.size > 48 * 1024 * 1024) throw new Error("ZIP 文件不能超过 48 MiB");
    let defaultID = file.name.replace(/\.zip$/i, "").toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "").slice(0, 63);
    if (!defaultID) defaultID = "imported-eval";
    const id = prompt("新数据集 ID（小写字母、数字、短横线）", defaultID);
    if (!id) return;
    const name = prompt("新数据集名称", file.name.replace(/\.zip$/i, ""));
    if (!name) return;
    const version = prompt("数据集版本", "0.1.0");
    if (!version) return;
    showToast("正在校验并导入 WeKnora ZIP…");
    const result = await api("/api/import/weknora", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ id, name, version, zip_base64: await fileAsBase64(file) }),
    });
    await loadDatasetList();
    await openDataset(result.data.project.id, true);
    showToast(`已导入 ${result.data.summary.question_count} 个问题和 ${result.data.summary.passage_count} 条语料`);
  } catch (error) { showToast(error.message, true); }
  finally { event.target.value = ""; }
});

window.addEventListener("beforeunload", (event) => {
  if (!state.dirty) return;
  event.preventDefault(); event.returnValue = "";
});

initializeConfigurationLayout();
Promise.all([loadDatasetList(), loadConnectionProfiles(), loadGenerationConnections(), loadWorkspaceSettings()]).catch((error) => showToast(error.message, true));
