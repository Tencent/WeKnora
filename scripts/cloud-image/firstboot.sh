#!/usr/bin/env bash
# firstboot.sh - 由 weknora-firstboot.service 在新实例首次开机时自动执行。
# 任务: 生成随机密钥写入 .env -> 启动容器 -> 输出凭证 -> 自删除。
set -euo pipefail

WEKNORA_DIR="${WEKNORA_DIR:-/opt/WeKnora}"
ENV_FILE="${WEKNORA_DIR}/.env"
CRED_FILE="/root/weknora-credentials.txt"
LOG_FILE="/var/log/weknora-firstboot.log"

exec >>"${LOG_FILE}" 2>&1
echo "==== firstboot started at $(date -Iseconds) ===="

if [[ ! -f "${ENV_FILE}" ]]; then
  echo "ERROR: ${ENV_FILE} not found"
  exit 1
fi

# 生成 32 字节强随机字符串(用于 AES-256 key, 必须刚好 32 字节)
gen32() { LC_ALL=C tr -dc 'A-Za-z0-9' </dev/urandom | head -c 32; }
# 通用密码: 24 字符, 不含 / + = (避免出现在 URL / sed 替换时出问题)
genpw() { LC_ALL=C tr -dc 'A-Za-z0-9' </dev/urandom | head -c 24; }

DB_PWD=$(genpw)
REDIS_PWD=$(genpw)
JWT=$(genpw)$(genpw)
SYS_AES=$(gen32)
TENANT_AES=$(gen32)

# 用 | 作 sed 分隔符避免冲突; 仅替换以 KEY= 开头的行
replace() {
  local key="$1" val="$2"
  if grep -qE "^${key}=" "${ENV_FILE}"; then
    sed -i "s|^${key}=.*|${key}=${val}|" "${ENV_FILE}"
  else
    echo "${key}=${val}" >>"${ENV_FILE}"
  fi
}

replace DB_PASSWORD     "${DB_PWD}"
replace REDIS_PASSWORD  "${REDIS_PWD}"
replace JWT_SECRET      "${JWT}"
replace SYSTEM_AES_KEY  "${SYS_AES}"
replace TENANT_AES_KEY  "${TENANT_AES}"
replace GIN_MODE        "release"

echo "env updated, starting docker compose..."
cd "${WEKNORA_DIR}"
/usr/bin/docker compose up -d

# 尝试拿公网 IP, 失败就用内网
PUB_IP=$(curl -fsS --max-time 5 https://ifconfig.me 2>/dev/null \
  || curl -fsS --max-time 5 https://api.ipify.org 2>/dev/null \
  || hostname -I | awk '{print $1}')

umask 077
cat >"${CRED_FILE}" <<INFO
========================================
  WeKnora 实例初始化完成
  生成时间: $(date -Iseconds)
========================================

访问地址 : http://${PUB_IP}

注册后如需关闭后续注册, 编辑 ${ENV_FILE}:
    DISABLE_REGISTRATION=true
然后执行:  cd ${WEKNORA_DIR} && docker compose up -d

下列随机凭证已写入 ${ENV_FILE}, 请妥善保存(仅 root 可读):
  DB_PASSWORD     = ${DB_PWD}
  REDIS_PASSWORD  = ${REDIS_PWD}
  JWT_SECRET      = ${JWT}
  SYSTEM_AES_KEY  = ${SYS_AES}
  TENANT_AES_KEY  = ${TENANT_AES}

安全建议:
  - 切勿直接对外暴露 5432 / 6379 / 9000 等基础设施端口
  - 仅用 80 / 443 对外服务, 必要时配置反向代理 + HTTPS

INFO

echo "credentials written to ${CRED_FILE}"

# 自删除: 关掉 unit + 删脚本 + 删 unit 文件
systemctl disable weknora-firstboot.service || true
rm -f /etc/systemd/system/weknora-firstboot.service
rm -f /usr/local/sbin/weknora-firstboot.sh
systemctl daemon-reload || true

echo "==== firstboot finished at $(date -Iseconds) ===="
