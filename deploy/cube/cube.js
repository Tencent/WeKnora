// Cube 运行时配置 (由 docker-compose.cube.yml 挂载到 /cube/conf/cube.js)。
//
// 连接配置来自 WeKnora 数据建模模块写入的 datasources.yaml
// (路径见 CUBE_DATASOURCES_FILE, 默认 /cube/conf/datasources.yaml):
//
//	datasources:
//	  - id: erp_main
//	    title: ERP 主库
//	    type: mysql
//	    host: ...
//	    port: 3306
//	    database: ...
//	    user: ...
//	    password: ...
//	    <扩展参数原样透传给驱动>
//
// 模型通过 data_source: <id> 选择连接 (Cube 多数据源机制);
// 未声明 data_source 的模型落在第一个连接上 (兼容单库部署)。
// 文件按 mtime 缓存, WeKnora 改写后立即生效, 无需重启。

const fs = require('fs');

function loadDatasources() {
  const file = process.env.CUBE_DATASOURCES_FILE || '/cube/conf/datasources.yaml';
  let yaml;
  try {
    yaml = require('js-yaml');
  } catch (e) {
    throw new Error('缺少 js-yaml 依赖, 无法解析 datasources.yaml');
  }
  let stat = null;
  try {
    stat = fs.statSync(file);
  } catch (e) {
    return { list: [], byId: {}, mtime: 0, file };
  }
  if (loadDatasources.cache && loadDatasources.cache.mtime === stat.mtimeMs) {
    return loadDatasources.cache;
  }
  const doc = yaml.load(fs.readFileSync(file, 'utf8')) || {};
  const list = (doc.datasources || []).filter((e) => e && e.id);
  const byId = {};
  for (const e of list) {
    byId[e.id] = e;
  }
  loadDatasources.cache = { list, byId, mtime: stat.mtimeMs, file };
  return loadDatasources.cache;
}

// 兼容直接传 securityContext 的调用形态 (Cube 回调参数是请求上下文)。
function secContextOf(ctx) {
  if (!ctx) return null;
  const sec = ctx.securityContext;
  if (sec && typeof sec === 'object' && Object.keys(sec).length > 0) return sec;
  if (ctx.groups || ctx.tenant_id || ctx.iat) return ctx;
  return null;
}

function datasourceForContext(ctx) {
  const ds = loadDatasources();
  if (ds.list.length === 0) {
    throw new Error('未配置任何数据源 (等待 WeKnora 数据建模模块写入 datasources.yaml)');
  }
  if (ds.list.length === 1) return ds.list[0];
  const name = ctx && ctx.dataSource;
  if (name && ds.byId[name]) return ds.byId[name];
  // Cube 内部编译 (如 RLS/accessPolicy) 有时以 dataSource='default' 调用,
  // 不带真实模型归属: 多数据源时无法确定, 回退第一个并告警 (真实查询仍按
  // 模型声明的 data_source 走, 不受影响)
  if (name && name !== 'default') {
    throw new Error(`data_source 无效: ${name} (可用: ${Object.keys(ds.byId).join(', ')})`);
  }
  if (ds.list.length > 1) {
    console.warn(`[cube.js] dataSource='${name}' 未指定/未知, 回退第一个数据源 ${ds.list[0].id}`);
  }
  return ds.list[0];
}

module.exports = {
  // JWT payload 中的 groups (WeKnora 数据组) 供模型 access_policy 鉴权
  contextToGroups: (securityContext) => {
    const sec = secContextOf(securityContext);
    const groups = sec && sec.groups;
    return Array.isArray(groups) ? groups : [];
  },

  // 单实例部署: schema 编译结果与用户身份无关, 全局共享一份缓存
  contextToAppId: () => 'semantic',
  contextToOrchestratorId: () => 'semantic',

  // 按模型声明的 data_source 返回 DriverConfig (取代 CUBEJS_DB_* 环境变量),
  // 返回 {type, ...} 让 Cube 自行推断方言; 扩展参数原样透传。
  driverFactory: (ctx) => {
    const ds = datasourceForContext(ctx);
    const config = {
      type: ds.type || 'mysql',
      host: ds.host,
      port: Number(ds.port || 3306),
      database: ds.database,
      user: ds.user,
      password: String(ds.password),
    };
    for (const [k, v] of Object.entries(ds)) {
      if (!(k in config) && k !== 'id' && k !== 'title') config[k] = v;
    }
    return config;
  },
};
