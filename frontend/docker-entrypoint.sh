#!/bin/sh

# Only emit whitelisted locale tags to avoid config.js injection from env values.
RUNTIME_DEFAULT_LOCALE=""
case "${DEFAULT_LOCALE:-}" in
  zh-CN|zh-TW|en-US|ru-RU|ko-KR|ja-JP) RUNTIME_DEFAULT_LOCALE="${DEFAULT_LOCALE}" ;;
esac

# 生成运行时配置文件，注入环境变量到前端
DEFAULT_FILE_MB=50
DEFAULT_SKILL_MB=256

# 数字校验：非数字/0/过大一律回退默认值，否则会生成 MAX_FILE_SIZE_MB: abc
# 或让 nginx 的 client_max_body_size 拿到非法值（M13）。
is_positive_number() {
  case "$1" in
    ''|*[!0-9]*) return 1 ;;
  esac
  [ "$1" -ge 1 ] && [ "$1" -le 10240 ]
}

FILE_MB=${MAX_FILE_SIZE_MB:-$DEFAULT_FILE_MB}
if ! is_positive_number "$FILE_MB"; then
  echo "[entrypoint] MAX_FILE_SIZE_MB 必须是 1-10240 的整数（收到「${MAX_FILE_SIZE_MB:-}」），回退 ${DEFAULT_FILE_MB}" >&2
  FILE_MB=$DEFAULT_FILE_MB
fi

SKILL_MB=${MAX_SKILL_BUNDLE_SIZE_MB:-$DEFAULT_SKILL_MB}
if ! is_positive_number "$SKILL_MB"; then
  echo "[entrypoint] MAX_SKILL_BUNDLE_SIZE_MB 必须是 1-10240 的整数（收到「${MAX_SKILL_BUNDLE_SIZE_MB:-}」），回退 ${DEFAULT_SKILL_MB}" >&2
  SKILL_MB=$DEFAULT_SKILL_MB
fi
if [ "$SKILL_MB" -lt "$FILE_MB" ]; then
  SKILL_MB=$FILE_MB
fi
if [ "$SKILL_MB" -gt 512 ]; then
  SKILL_MB=512
fi

# 品牌配置：默认值与 frontend/public/config.js 保持一致（actionColor 不再写死，
# 由前端按 primaryColor 派生；显式配置时仍可覆盖）。
# BRAND_CONFIG_JSON 不直接拼进 JS：先转成 base64 字符串字面量，浏览器端再
# JSON.parse 校验。这样即使运维传了尾逗号、坏引号或多行文本，生成的
# config.js 本身仍是合法 JS，坏配置只回退默认品牌，不会让整份运行时配置失效（M13）。
DEFAULT_BRAND_JSON='{"productName":"小爱知识库","logoUrl":"/brand/allstar-logo.png","logoWidth":150,"faviconUrl":"/favicon.ico","primaryColor":"#0EA5E9","loginSubtitle":"金蝶产品知识，随问随答","chatDisclaimer":"回答由 AI 根据知识库生成，仅供参考","productLines":[{"id":"kingdee-ai-suite","name":"金蝶 AI 套件","description":"AI 套件开通、配置、使用问题","agentId":"builtin-quick-answer","samples":["如何开通智能问数？","知识库容量上限是多少？"]},{"id":"kingdee-ai-starry-sky","name":"金蝶 AI 星空","description":"星空云 ERP 财务、供应链操作问题","agentId":"builtin-quick-answer","samples":["凭证过账报错怎么处理？","期末结账要检查哪些项？"]}]}'
BRAND_EXPR="$DEFAULT_BRAND_JSON"

if [ -n "${BRAND_CONFIG_JSON:-}" ]; then
  BRAND_JSON_TRIMMED=$(printf '%s' "$BRAND_CONFIG_JSON" | sed 's/^[[:space:]]*//; s/[[:space:]]*$//')
  case "$BRAND_JSON_TRIMMED" in
    \{*\})
      if printf '%s' "$BRAND_JSON_TRIMMED" | grep -q '"productName"'; then
        BRAND_B64=$(printf '%s' "$BRAND_JSON_TRIMMED" | base64 | tr -d '\n')
        if [ -n "$BRAND_B64" ]; then
          BRAND_EXPR=$(cat <<BRAND_EXPR_EOF
(function () {
      try {
        var binary = atob("${BRAND_B64}");
        var bytes = new Uint8Array(binary.length);
        for (var i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
        return JSON.parse(new TextDecoder("utf-8").decode(bytes));
      } catch (error) {
        console.warn("[entrypoint] BRAND_CONFIG_JSON 不是合法 JSON，已回退默认品牌配置", error);
        return ${DEFAULT_BRAND_JSON};
      }
    })()
BRAND_EXPR_EOF
)
        else
          echo "[entrypoint] BRAND_CONFIG_JSON 无法编码，回退默认品牌配置" >&2
        fi
      else
        echo "[entrypoint] BRAND_CONFIG_JSON 里找不到 productName，回退默认品牌配置" >&2
      fi
      ;;
    *)
      echo "[entrypoint] BRAND_CONFIG_JSON 必须以 { 开头、以 } 结尾，回退默认品牌配置" >&2
      ;;
  esac
fi

cat > /usr/share/nginx/html/config.js << EOF
window.__RUNTIME_CONFIG__ = {
  MAX_FILE_SIZE_MB: ${FILE_MB},
  MAX_SKILL_BUNDLE_SIZE_MB: ${SKILL_MB},
  DEFAULT_LOCALE: "${RUNTIME_DEFAULT_LOCALE}",
  BRAND: ${BRAND_EXPR}
};
EOF

# 处理 nginx 配置。
# 两个上限分开注入：全站保持知识库的 MAX_FILE_SIZE，只有技能 zip 上传的两条
# 集合路由放宽到 MAX_SKILL_BUNDLE_SIZE（不含 /install、PATCH 等子路径）。
# 合成一个全站上限会让每个上传端点都能收到技能包那么大的 body。
export MAX_FILE_SIZE=${FILE_MB}M
export MAX_SKILL_BUNDLE_SIZE=${SKILL_MB}M
export APP_HOST=${APP_HOST:-app}
export APP_PORT=${APP_PORT:-8080}
export APP_SCHEME=${APP_SCHEME:-http}
envsubst '${MAX_FILE_SIZE} ${MAX_SKILL_BUNDLE_SIZE} ${APP_HOST} ${APP_PORT} ${APP_SCHEME}' \
  < /etc/nginx/templates/default.conf.template > /etc/nginx/conf.d/default.conf

# 启动 nginx
exec nginx -g 'daemon off;'
