#!/bin/sh
set -e

normalize_base_path() {
  p="${1:-}"
  p="$(echo "$p" | sed 's#^/*##; s#/*$##')"
  if [ -z "$p" ]; then
    echo ""
  else
    echo "/$p"
  fi
}

export WEKNORA_BASE_PATH="$(normalize_base_path "${WEKNORA_BASE_PATH:-}")"

# 严格的路径安全正则校验
if [ -n "$WEKNORA_BASE_PATH" ] && ! echo "$WEKNORA_BASE_PATH" | grep -Eq '^(/[A-Za-z0-9._~-]+)+$'; then
  echo "[ERROR] Invalid WEKNORA_BASE_PATH format: $WEKNORA_BASE_PATH"
  echo "[ERROR] Allowed examples: /weknora, /kb, /internal/weknora"
  exit 1
fi

# 动态产生不带斜杠的子路径 301 重定向语句
if [ -n "$WEKNORA_BASE_PATH" ]; then
  export WEKNORA_BASE_PATH_REDIRECT_BLOCK="location = ${WEKNORA_BASE_PATH} { return 301 ${WEKNORA_BASE_PATH}/; }"
else
  export WEKNORA_BASE_PATH_REDIRECT_BLOCK=""
fi

# 生成运行时配置文件，注入环境变量到前端
cat > /usr/share/nginx/html/config.js << EOF
window.__RUNTIME_CONFIG__ = {
  MAX_FILE_SIZE_MB: ${MAX_FILE_SIZE_MB:-50}
};
EOF

# 处理 nginx 配置
export MAX_FILE_SIZE=${MAX_FILE_SIZE_MB:-50}M
export APP_HOST=${APP_HOST:-app}
export APP_PORT=${APP_PORT:-8080}
export APP_SCHEME=${APP_SCHEME:-http}

envsubst '${MAX_FILE_SIZE} ${APP_HOST} ${APP_PORT} ${APP_SCHEME} ${WEKNORA_BASE_PATH} ${WEKNORA_BASE_PATH_REDIRECT_BLOCK}' \
  < /etc/nginx/templates/default.conf.template \
  > /etc/nginx/conf.d/default.conf

# 如果提供了参数命令（如 nginx -t），则在写入配置后直接运行该命令并退出
if [ "$#" -gt 0 ]; then
  exec "$@"
fi

exec nginx -g 'daemon off;'
