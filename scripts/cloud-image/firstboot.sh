#!/usr/bin/env bash
# firstboot.sh - 由 weknora-firstboot.service 在新实例首次开机时自动执行。
# 任务: 生成随机密钥写入 .env -> 启动容器 -> 输出凭证 -> 标记完成 + 自禁用。
#
# 幂等性策略:
#   1) 一上来先写 ${MARKER} 标记 "已生成过密钥",
#      之后即便 docker compose 失败导致脚本中断, 重启后由 unit 的
#      ConditionPathExists=!${MARKER} 拦住, 不会再生成新密钥覆盖 .env。
#      (旧密钥已经写进 postgres 数据卷, 二次生成会导致数据库永久无法登录)
#   2) 失败时 unit 标记 failed, 用户可手动 docker compose up -d 恢复;
#      凭证仍可在 ${ENV_FILE} 中查到。
set -euo pipefail

WEKNORA_DIR="${WEKNORA_DIR:-/opt/WeKnora}"
ENV_FILE="${WEKNORA_DIR}/.env"
ENV_TEMPLATE="${WEKNORA_DIR}/.env.example"
CRED_FILE="/root/weknora-credentials.txt"
LOG_FILE="/var/log/weknora-firstboot.log"
MARKER="${WEKNORA_DIR}/.firstboot.done"

# 提前打开 LOG_FILE, 同时把 stderr 也复制一份到 systemd journal 方便调试
# (单纯 exec >> LOG_FILE 时, 脚本一开始的失败会因为 stderr 已被吞掉而看不到)
mkdir -p "$(dirname "${LOG_FILE}")"
exec > >(tee -a "${LOG_FILE}") 2>&1
echo "==== firstboot started at $(date -Iseconds) ===="

if [[ -f "${MARKER}" ]]; then
  echo "marker ${MARKER} exists, skip (already initialized)"
  exit 0
fi

# cleanup.sh 不再保留 .env, 这里从 .env.example 拷贝模板再做替换。
# 这样保证 firstboot 之前不会有任何含明文默认密码的 .env 让 weknora.service
# 抢先把 postgres 数据卷用错的密码初始化掉。
if [[ ! -f "${ENV_FILE}" ]]; then
  if [[ -f "${ENV_TEMPLATE}" ]]; then
    echo "creating ${ENV_FILE} from ${ENV_TEMPLATE}"
    cp "${ENV_TEMPLATE}" "${ENV_FILE}"
  else
    echo "ERROR: neither ${ENV_FILE} nor ${ENV_TEMPLATE} found"
    exit 1
  fi
fi

DOCKER_BIN="$(command -v docker || true)"
if [[ -z "${DOCKER_BIN}" ]]; then
  echo "ERROR: docker binary not found in PATH"
  exit 1
fi

# 生成 32 字节强随机字符串(用于 AES-256 key, 必须刚好 32 字节)
# 用 `() ... ()` 启子 shell 并关闭 pipefail: head 只读 N 字节后关闭 stdin,
# tr 会收到 SIGPIPE (退出码 141), 在 `set -o pipefail` 下整条管道被判失败,
# 触发顶层 `set -e` 把 firstboot.sh 8ms exit 1 干掉。
gen32() ( set +o pipefail; LC_ALL=C tr -dc 'A-Za-z0-9' </dev/urandom | head -c 32 )
# 通用密码: 24 字符, 不含 / + = (避免出现在 URL / sed 替换时出问题)
genpw() ( set +o pipefail; LC_ALL=C tr -dc 'A-Za-z0-9' </dev/urandom | head -c 24 )

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

# 把 prepare.sh 阶段记录在 .cloud-image-meta 里的 WEKNORA_REF 还原为 .env 的
# WEKNORA_VERSION, 否则 docker compose 会落回 :latest 默认值, 导致镜像版本
# 与 prepare 时拉取的版本不一致。
META_FILE="${WEKNORA_DIR}/.cloud-image-meta"
if [[ -f "${META_FILE}" ]]; then
  META_REF=$(grep -E '^WEKNORA_REF=' "${META_FILE}" | tail -1 | cut -d= -f2- || true)
  if [[ -n "${META_REF}" ]]; then
    replace WEKNORA_VERSION "${META_REF}"
    echo "restored WEKNORA_VERSION=${META_REF} from ${META_FILE}"
  fi
fi

# 关键: .env 改完立刻写 marker。
# 这之后即便 docker compose up 失败, 重启也不会再次重写 .env,
# 防止与 postgres 已经持久化的密码不一致。
umask 077
touch "${MARKER}"
chmod 0600 "${MARKER}"

echo "env updated, marker written, starting docker compose..."
cd "${WEKNORA_DIR}"
"${DOCKER_BIN}" compose up -d

# 尝试拿公网 IP, 失败就用内网
PUB_IP=$(curl -fsS --max-time 5 https://ifconfig.me 2>/dev/null \
  || curl -fsS --max-time 5 https://api.ipify.org 2>/dev/null \
  || hostname -I | awk '{print $1}')

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

注意:
  - 本文件只反映首启时刻的密钥, 后续以 ${ENV_FILE} 为准。
  - 切勿直接对外暴露 5432 / 6379 / 9000 等基础设施端口。
  - 仅用 80 / 443 对外服务, 必要时配置反向代理 + HTTPS。

INFO

echo "credentials written to ${CRED_FILE}"

# 仅停掉 unit, 不删 unit 文件 (否则当前正在运行的 oneshot 可能被 systemd 标记 failed)。
# 下次重启时, weknora-firstboot.service 通过 ConditionPathExists=!${MARKER} 自动跳过。
systemctl disable weknora-firstboot.service || true

echo "==== firstboot finished at $(date -Iseconds) ===="
