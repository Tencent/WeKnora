#!/usr/bin/env bash
# prepare.sh - 在干净的 Linux 实例上部署 WeKnora 运行时, 用于制作云镜像模板。
# 不需要 clone 整个 WeKnora 仓库, 只下载 4 个运行时文件 (~100KB)。
# 兼容: Ubuntu / Debian / CentOS / Rocky / TencentOS 等带 systemd + Docker 的发行版。
# 使用方式:  sudo bash prepare.sh
# 可调环境变量:
#   WEKNORA_REF    要拉取的 git ref (tag / branch / commit), 默认 main
#   WEKNORA_DIR    部署目录, 默认 /opt/WeKnora
#   WEKNORA_REPO   仓库地址, 默认 https://github.com/Tencent/WeKnora
set -euo pipefail

WEKNORA_REF="${WEKNORA_REF:-main}"
WEKNORA_DIR="${WEKNORA_DIR:-/opt/WeKnora}"
WEKNORA_REPO="${WEKNORA_REPO:-https://github.com/Tencent/WeKnora}"
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"

if [[ "${EUID}" -ne 0 ]]; then
  echo "[prepare] 请使用 sudo 或 root 运行" >&2
  exit 1
fi

echo "[prepare] 1/6 安装 Docker 与依赖"
if ! command -v docker >/dev/null 2>&1; then
  curl -fsSL https://get.docker.com | bash
fi
systemctl enable --now docker

if ! docker compose version >/dev/null 2>&1; then
  if command -v apt-get >/dev/null 2>&1; then
    apt-get update -y
    apt-get install -y docker-compose-plugin curl tar
  elif command -v yum >/dev/null 2>&1; then
    yum install -y docker-compose-plugin curl tar
  fi
fi

echo "[prepare] 2/6 拉取 WeKnora 运行时文件 (ref=${WEKNORA_REF})"
# 只下载实际需要的 4 个文件, 不 clone 整个仓库 (~MB 级 -> ~KB 级)
mkdir -p "${WEKNORA_DIR}/config" "${WEKNORA_DIR}/skills"

tmp=$(mktemp -d)
trap 'rm -rf "${tmp}"' EXIT

curl -fsSL "${WEKNORA_REPO}/archive/${WEKNORA_REF}.tar.gz" -o "${tmp}/repo.tar.gz"
# 仅解压需要的路径, 显著加速且省空间
tar -xzf "${tmp}/repo.tar.gz" -C "${tmp}" \
  --wildcards \
  '*/docker-compose.yml' \
  '*/.env.example' \
  '*/config/config.yaml' \
  '*/skills/preloaded'
src=$(find "${tmp}" -maxdepth 1 -mindepth 1 -type d -name 'WeKnora-*' | head -1)
if [[ -z "${src}" ]]; then
  echo "[prepare] 解压失败, 未找到 WeKnora-* 目录" >&2
  exit 1
fi

cp    "${src}/docker-compose.yml" "${WEKNORA_DIR}/"
cp    "${src}/.env.example"       "${WEKNORA_DIR}/"
cp    "${src}/config/config.yaml" "${WEKNORA_DIR}/config/"
rm -rf "${WEKNORA_DIR}/skills/preloaded"
cp -r "${src}/skills/preloaded"   "${WEKNORA_DIR}/skills/"

# 记录元信息, 供 firstboot / 升级时参考
cat >"${WEKNORA_DIR}/.cloud-image-meta" <<EOF
WEKNORA_REF=${WEKNORA_REF}
WEKNORA_REPO=${WEKNORA_REPO}
PREPARED_AT=$(date -Iseconds)
EOF

echo "[prepare] 3/6 准备 .env (默认值, firstboot 会替换为随机密钥)"
cd "${WEKNORA_DIR}"
[[ -f .env ]] || cp .env.example .env
sed -i 's/^GIN_MODE=.*/GIN_MODE=release/' .env || true

echo "[prepare] 4/6 拉取并启动默认 5 个常驻容器 (frontend/app/docreader/postgres/redis)"
docker compose pull
docker compose up -d

# 提前 pull sandbox 镜像 (Agent Skills 运行时由 app 按需 docker run, 非常驻)
# 不预拉的话, 用户首次跑 Skill 会卡在下载
echo "[prepare] 4.5/6 预拉 sandbox 镜像 (Agent Skills 用, 非常驻)"
docker compose --profile full pull sandbox || true

# 其他向量库 / 可观测组件 (qdrant, milvus, weaviate, doris, neo4j, langfuse-*, minio, jaeger, dex)
# 不预拉, 体积可省 5-15GB. 用户如需启用:
#   cd /opt/WeKnora && docker compose --profile <name> up -d

echo "[prepare] 5/6 安装 systemd 单元"
install -m 0644 "${SCRIPT_DIR}/systemd/weknora.service"           /etc/systemd/system/weknora.service
install -m 0644 "${SCRIPT_DIR}/systemd/weknora-firstboot.service" /etc/systemd/system/weknora-firstboot.service
install -m 0755 "${SCRIPT_DIR}/firstboot.sh"                      /usr/local/sbin/weknora-firstboot.sh

systemctl daemon-reload
systemctl enable weknora.service
systemctl enable weknora-firstboot.service

echo "[prepare] 6/6 完成"
echo
echo "  WeKnora 运行时已部署到 ${WEKNORA_DIR}"
echo "    docker-compose.yml / config/config.yaml / skills/preloaded / .env"
echo "  版本: ${WEKNORA_REF}  (见 ${WEKNORA_DIR}/.cloud-image-meta)"
echo
echo "  打开浏览器访问  http://<本机公网IP>  验证功能"
echo
echo "  验证通过后执行清理并制作镜像:"
echo "      sudo bash ${SCRIPT_DIR}/cleanup.sh"
