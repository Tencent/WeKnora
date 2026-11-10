#!/usr/bin/env bash
# cleanup.sh - 在制作云镜像前清理私密数据。
# 警告: 本脚本会删除 SSH 公钥、清空数据库与日志，最后会自动关机。
# 执行后请直接在云控制台「制作镜像 / 创建快照 / 创建 AMI」，不要再 SSH 进来。
set -euo pipefail

WEKNORA_DIR="${WEKNORA_DIR:-/opt/WeKnora}"

if [[ "${EUID}" -ne 0 ]]; then
  echo "[cleanup] 请使用 sudo 或 root 运行" >&2
  exit 1
fi

read -r -p "[cleanup] 该操作不可逆，确认继续? 输入 YES 继续: " ans
if [[ "${ans}" != "YES" ]]; then
  echo "[cleanup] 已取消"
  exit 0
fi

echo "[cleanup] 1/8 停止 WeKnora 容器"
COMPOSE_PROJECT=""
if [[ -d "${WEKNORA_DIR}" ]]; then
  cd "${WEKNORA_DIR}"
  # 优先用 compose ls 拿到真实 project 名 (默认是目录名小写, 如 weknora)
  COMPOSE_PROJECT="$(docker compose ls --format json 2>/dev/null \
    | grep -oE '"Name":"[^"]+"' | head -1 | cut -d'"' -f4 || true)"
  docker compose down -v --remove-orphans || true
fi

echo "[cleanup] 2/8 清空 WeKnora 业务数据 + 首启 marker / 日志"
if [[ -d "${WEKNORA_DIR}" ]]; then
  rm -rf "${WEKNORA_DIR}/data"/* "${WEKNORA_DIR}/logs"/* 2>/dev/null || true
  # 故意不在这里重建 .env: 让镜像里 .env 缺失, 任何在 firstboot 之前
  # 起来的 docker compose 都会因找不到 .env 而失败, 避免明文默认密码
  # (postgres123!@# 之类) 把 postgres 数据卷初始化坏。
  # firstboot.sh 自己会从 .env.example 拷贝并替换密钥。
  rm -f "${WEKNORA_DIR}/.env" "${WEKNORA_DIR}/.firstboot.done"
fi
rm -f /root/weknora-credentials.txt /var/log/weknora-firstboot.log

echo "[cleanup] 3/8 清理残留 docker 卷与构建缓存"
# 严格按 compose project 名前缀匹配, 避免误伤同宿主上其它 postgres/redis 卷。
if [[ -n "${COMPOSE_PROJECT}" ]]; then
  docker volume ls -q --filter "label=com.docker.compose.project=${COMPOSE_PROJECT}" \
    | xargs -r docker volume rm -f || true
fi
# 注意: 这里只清"卷 / 已停容器 / 构建缓存", 绝对不能清镜像。
# 此前用过 `docker system prune -af --volumes`, 会把 prepare.sh 预拉的
# wechatopenai/weknora-* 等镜像一并清掉, 导致基于镜像建出来的新实例
# 在 firstboot 时还要重新从 Docker Hub 拉数 GB 镜像, 完全违背预装初衷。
docker container prune -f      || true
docker builder    prune -af    || true
# 只清未挂载的 dangling 卷 (此时 compose down -v 已清掉业务卷)
docker volume     prune -f     || true

echo "[cleanup] 4/8 清空系统日志"
journalctl --rotate || true
journalctl --vacuum-time=1s || true
find /var/log -type f \( -name '*.log' -o -name '*.gz' -o -name '*.[0-9]' \) -print0 \
  | xargs -0 -r truncate -s 0 || true
find /var/log -type f \( -name '*.gz' -o -name '*.[0-9]' \) -print0 \
  | xargs -0 -r rm -f || true

echo "[cleanup] 5/8 清理 SSH 历史与授权 key（执行后将无法 SSH 进来）"
rm -f /root/.ssh/authorized_keys /root/.ssh/known_hosts /root/.bash_history
for d in /home/*; do
  [[ -d "$d" ]] || continue
  rm -f "$d/.ssh/authorized_keys" "$d/.ssh/known_hosts" "$d/.bash_history"
done
find / -xdev -type f \( -name 'id_rsa*' -o -name '*.pem' -o -name '*.key' \) \
  -not -path '/etc/ssl/*' -not -path '/usr/*' -not -path '/var/lib/docker/*' 2>/dev/null \
  | tee /tmp/cleanup-secrets-found.txt || true
echo "[cleanup]   ↑ 上面是疑似遗留的密钥文件，必要时人工再核对"

echo "[cleanup] 6/8 重置 cloud-init / machine-id（让新实例拿到新 ID）"
cloud-init clean --logs --seed 2>/dev/null || true
truncate -s 0 /etc/machine-id || true
rm -f /var/lib/dbus/machine-id || true

echo "[cleanup] 7/8 清理 apt / tmp"
if command -v apt-get >/dev/null 2>&1; then
  apt-get clean
  rm -rf /var/lib/apt/lists/*
fi
rm -rf /tmp/* /var/tmp/* /root/.cache /home/*/.cache 2>/dev/null || true

echo "[cleanup] 8/8 同步磁盘并关机"
history -c || true
sync
echo
echo "  即将关机。关机完成后请到云控制台执行「制作镜像 / 创建快照 / 创建 AMI」。"
echo
sleep 3
poweroff
