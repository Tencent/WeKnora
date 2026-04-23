/**
 * 阻断 tdesign-icons-vue-next 对外网 CDN 的 iconfont 请求。
 *
 * 背景（issue #867 / #897）：
 *   tdesign-icons-vue-next 的 Icon / IconFont 组件在 onMounted 时会通过
 *   `checkScriptAndLoad` / `checkLinkAndLoad` 往 document 里注入指向
 *   `https://tdesign.gtimg.com/icon/<version>/fonts/index.(js|css)` 的
 *   <script> / <link>。在无外网环境下请求会失败，导致图标全部不渲染。
 *
 * 处理方式：在 Vue app 挂载之前预先插入符合 tdesign 匹配规则的占位节点，
 *   让 `checkScriptAndLoad` / `checkLinkAndLoad` 的去重判断命中、直接返回，
 *   不再追加指向 CDN 的真实节点。
 *
 *   - 匹配选择器来源：tdesign-icons-vue-next/esm/utils/check-url-and-load.js
 *       `.t-svg-js-stylesheet--unique-class[src="<url>"]`
 *       `.t-iconfont-stylesheet--unique-class[href="<url>"]`
 *   - 占位 <script> / <link> 使用非标准 type / rel，浏览器不会真正发起网络请求。
 *
 * SVG sprite 的符号则通过 index.html 中的 <script src="/tdesign-icons/.../index.js"></script>
 * 在本地提前注册，因此 <t-icon name="..."> 仍可正常渲染。
 */

const SVG_SCRIPT_CLASS = "t-svg-js-stylesheet--unique-class";
const ICONFONT_LINK_CLASS = "t-iconfont-stylesheet--unique-class";

// 对齐 tdesign-icons-vue-next 0.4.x 内部硬编码的地址；多版本号并存以兼容潜在升级。
const BLOCKED_SCRIPT_URLS = [
  "https://tdesign.gtimg.com/icon/0.4.0/fonts/index.js",
  "https://tdesign.gtimg.com/icon/0.4.1/fonts/index.js",
];

const BLOCKED_LINK_URLS = [
  "https://tdesign.gtimg.com/icon/0.4.0/fonts/index.css",
  "https://tdesign.gtimg.com/icon/0.4.1/fonts/index.css",
];

let installed = false;

export function installTDesignIconOfflineGuard(): void {
  if (installed || typeof document === "undefined") return;
  installed = true;

  const body = document.body;
  if (!body) {
    document.addEventListener(
      "DOMContentLoaded",
      () => installTDesignIconOfflineGuard(),
      { once: true },
    );
    installed = false;
    return;
  }

  BLOCKED_SCRIPT_URLS.forEach((src) => {
    const exists = document.querySelector(
      `script.${SVG_SCRIPT_CLASS}[src="${src}"]`,
    );
    if (exists) return;
    const stub = document.createElement("script");
    stub.setAttribute("class", SVG_SCRIPT_CLASS);
    stub.setAttribute("src", src);
    // 非标准 MIME 类型会让浏览器跳过脚本的 fetch/执行阶段
    stub.setAttribute("type", "text/no-load");
    stub.setAttribute("data-weknora-blocked-cdn", "tdesign-icons");
    body.appendChild(stub);
  });

  BLOCKED_LINK_URLS.forEach((href) => {
    const exists = document.querySelector(
      `link.${ICONFONT_LINK_CLASS}[href="${href}"]`,
    );
    if (exists) return;
    const stub = document.createElement("link");
    stub.setAttribute("class", ICONFONT_LINK_CLASS);
    stub.setAttribute("href", href);
    // 不声明 rel="stylesheet"，浏览器不会发起样式表请求
    stub.setAttribute("rel", "preload-blocked");
    stub.setAttribute("data-weknora-blocked-cdn", "tdesign-icons");
    document.head.appendChild(stub);
  });
}
