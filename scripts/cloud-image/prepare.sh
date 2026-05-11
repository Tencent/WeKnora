#!/usr/bin/env bash
# prepare.sh - 在干净的 Linux 实例上部署 WeKnora 运行时, 用于制作云镜像模板。
# 不需要 clone 整个 WeKnora 仓库, 只下载 4 个运行时文件 (~100KB)。
# 兼容: Ubuntu / Debian / CentOS / Rocky / TencentOS 等带 systemd + Docker 的发行版。
# 使用方式:  sudo bash prepare.sh
# 可调环境变量:
#   WEKNORA_REF              要拉取的 git ref (tag / branch / commit), 默认 main
#   WEKNORA_DIR              部署目录, 默认 /opt/WeKnora
#   WEKNORA_REPO             仓库地址, 默认 https://github.com/Tencent/WeKnora
#   WEKNORA_GH_PROXY         GitHub 加速前缀, 默认空。中国大陆机器可设
#                            https://gh-proxy.com/ 或 https://ghfast.top/
#                            (实际下载地址变成 ${WEKNORA_GH_PROXY}${WEKNORA_REPO}/archive/...)
#   DOCKER_INSTALL_MIRROR    Docker 安装包镜像源, 默认空 (走 get.docker.com)。
#                            中国大陆机器境外 CDN 不通时设为, 例如:
#                              https://mirrors.tencent.com/docker-ce/linux/ubuntu
#                              https://mirrors.aliyun.com/docker-ce/linux/ubuntu
#                            会改用 apt + docker-ce 官方仓库镜像安装,
#                            含 docker-ce / containerd.io / docker-compose-plugin,
#                            完全不访问 get.docker.com。仅支持 apt 系发行版。
#   DOCKER_REGISTRY_MIRROR   Docker Hub 加速器, 默认空。腾讯云内网可设
#                            https://mirror.ccs.tencentyun.com
#                            (会写入 /etc/docker/daemon.json 并重启 docker)
#   PRUNE_OLD_IMAGES         升级场景下是否清理 dangling / 旧版本 tag 镜像,
#                            默认 false。设为 true 时在拉新镜像之后执行
#                            `docker image prune -af`, 把没有容器引用的镜像
#                            (含旧 WEKNORA_VERSION 的 wechatopenai/weknora-*)
#                            一次性删掉, 减少要打进云镜像的体积。
set -euo pipefail

WEKNORA_REF="${WEKNORA_REF:-main}"
WEKNORA_DIR="${WEKNORA_DIR:-/opt/WeKnora}"
WEKNORA_REPO="${WEKNORA_REPO:-https://github.com/Tencent/WeKnora}"
WEKNORA_GH_PROXY="${WEKNORA_GH_PROXY:-}"
DOCKER_INSTALL_MIRROR="${DOCKER_INSTALL_MIRROR:-}"
DOCKER_REGISTRY_MIRROR="${DOCKER_REGISTRY_MIRROR:-}"
PRUNE_OLD_IMAGES="${PRUNE_OLD_IMAGES:-false}"
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"

if [[ "${EUID}" -ne 0 ]]; then
  echo "[prepare] 请使用 sudo 或 root 运行" >&2
  exit 1
fi

# 通过镜像源 apt 安装 docker-ce 全家桶 (含 compose-plugin)。
# 用于中国大陆云主机直连 get.docker.com 被 RST 的场景。
install_docker_via_apt_mirror() {
  local mirror="$1"
  if ! command -v apt-get >/dev/null 2>&1; then
    echo "[prepare] DOCKER_INSTALL_MIRROR 目前仅支持 apt 系发行版 (Ubuntu/Debian)" >&2
    return 1
  fi
  apt-get update -y
  apt-get install -y ca-certificates curl gnupg lsb-release
  install -m 0755 -d /etc/apt/keyrings
  curl -fsSL "${mirror%/}/gpg" | gpg --dearmor --yes -o /etc/apt/keyrings/docker.gpg
  chmod a+r /etc/apt/keyrings/docker.gpg
  local arch codename
  arch="$(dpkg --print-architecture)"
  codename="$(lsb_release -cs)"
  echo "deb [arch=${arch} signed-by=/etc/apt/keyrings/docker.gpg] ${mirror%/} ${codename} stable" \
    > /etc/apt/sources.list.d/docker.list
  apt-get update -y
  apt-get install -y docker-ce docker-ce-cli containerd.io \
                     docker-buildx-plugin docker-compose-plugin curl tar
}

echo "[prepare] 1/6 安装 Docker 与依赖"
if ! command -v docker >/dev/null 2>&1; then
  if [[ -n "${DOCKER_INSTALL_MIRROR}" ]]; then
    echo "[prepare]   通过 ${DOCKER_INSTALL_MIRROR} 走 apt 安装 docker-ce (跳过 get.docker.com)"
    install_docker_via_apt_mirror "${DOCKER_INSTALL_MIRROR}"
  else
    curl -fsSL https://get.docker.com | bash
  fi
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

# 可选: 配置 Docker Hub 加速器, 解决直连 registry-1.docker.io 超时的场景
# (典型: 中国大陆云主机 / 内网受限环境)。仅在用户显式传入时才动 daemon.json。
if [[ -n "${DOCKER_REGISTRY_MIRROR}" ]]; then
  echo "[prepare] 1.5/6 配置 Docker Hub 加速器: ${DOCKER_REGISTRY_MIRROR}"
  mkdir -p /etc/docker
  # 已存在的 daemon.json 走 python 合并, 避免覆盖用户其它配置
  if [[ -s /etc/docker/daemon.json ]] && command -v python3 >/dev/null 2>&1; then
    python3 - "$DOCKER_REGISTRY_MIRROR" <<'PY'
import json, sys, pathlib
p = pathlib.Path("/etc/docker/daemon.json")
mirror = sys.argv[1]
try:
    cfg = json.loads(p.read_text())
except Exception:
    cfg = {}
mirrors = cfg.get("registry-mirrors") or []
if mirror not in mirrors:
    mirrors.insert(0, mirror)
cfg["registry-mirrors"] = mirrors
p.write_text(json.dumps(cfg, indent=2) + "\n")
PY
  else
    cat >/etc/docker/daemon.json <<EOF
{
  "registry-mirrors": ["${DOCKER_REGISTRY_MIRROR}"]
}
EOF
  fi
  systemctl restart docker
  # 给 docker daemon 一点重启时间, 避免下面 docker compose pull 立刻拿到 EOF
  for _ in 1 2 3 4 5; do
    docker info >/dev/null 2>&1 && break
    sleep 1
  done
fi

echo "[prepare] 2/6 拉取 WeKnora 运行时文件 (ref=${WEKNORA_REF})"
# 只下载实际需要的 4 个文件, 不 clone 整个仓库 (~MB 级 -> ~KB 级)
mkdir -p "${WEKNORA_DIR}/config" "${WEKNORA_DIR}/skills"

tmp=$(mktemp -d)
trap 'rm -rf "${tmp}"' EXIT

tarball_url="${WEKNORA_GH_PROXY}${WEKNORA_REPO}/archive/${WEKNORA_REF}.tar.gz"
echo "[prepare]   tarball: ${tarball_url}"
curl -fsSL "${tarball_url}" -o "${tmp}/repo.tar.gz"
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

# 把 WEKNORA_VERSION 与 WEKNORA_REF 对齐, 让 docker compose 拉取与 ref 一致的
# 镜像 tag。无条件覆盖, 避免 .env 残留上一次 prepare 留下的旧版本号。
# Docker Hub 上 wechatopenai/weknora-* 的 tag 实际值就是 git ref 原样
# (`main` / `v0.5.1`), 因此这里不剥 v、也不映射到 latest。
WEKNORA_VERSION_VAL="${WEKNORA_REF}"
if grep -qE '^WEKNORA_VERSION=' .env; then
  sed -i "s|^WEKNORA_VERSION=.*|WEKNORA_VERSION=${WEKNORA_VERSION_VAL}|" .env
else
  echo "WEKNORA_VERSION=${WEKNORA_VERSION_VAL}" >>.env
fi
echo "[prepare]   -> WEKNORA_VERSION=${WEKNORA_VERSION_VAL}"

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

# 升级场景: 清理旧版本 tag 的 wechatopenai/weknora-* 镜像。
# 默认关闭, 保留回滚路径; 制作镜像前显式打开以减小体积。
#
# 注意: 不用 `docker image prune -af`!
# sandbox 镜像在 compose 里只 pull 不 up (Agent Skills 由 app 按需 docker run),
# 没有任何容器引用它, 一旦 `prune -a` 会把当前版本的 sandbox 一起删掉,
# 反而违背 prepare.sh 4.5 步预拉 sandbox 的目的。
# 这里精确按 tag 比对, 只删 wechatopenai/weknora-* 仓库下、tag 不等于当前
# WEKNORA_VERSION 的镜像, 基础设施镜像 (paradedb / redis) 不动。
if [[ "${PRUNE_OLD_IMAGES,,}" == "true" || "${PRUNE_OLD_IMAGES}" == "1" ]]; then
  echo "[prepare] 4.6/6 清理 wechatopenai/weknora-* 仓库下旧版本镜像 (PRUNE_OLD_IMAGES=true, keep=${WEKNORA_VERSION_VAL})"
  docker image ls --format '{{.Repository}}:{{.Tag}}' \
    | grep -E '^wechatopenai/weknora-' \
    | grep -vE ":${WEKNORA_VERSION_VAL}\$" \
    | xargs -r docker rmi -f 2>/dev/null || true
fi

echo "[prepare] 5/6 安装 systemd 单元"
# 探测 docker 二进制路径, 不同发行版可能在 /usr/bin 或 /usr/local/bin
DOCKER_BIN="$(command -v docker)"
if [[ -z "${DOCKER_BIN}" ]]; then
  echo "[prepare] 未找到 docker 二进制" >&2
  exit 1
fi
echo "[prepare]   docker binary: ${DOCKER_BIN}"

install -m 0644 "${SCRIPT_DIR}/systemd/weknora.service"           /etc/systemd/system/weknora.service
install -m 0644 "${SCRIPT_DIR}/systemd/weknora-firstboot.service" /etc/systemd/system/weknora-firstboot.service
install -m 0755 "${SCRIPT_DIR}/firstboot.sh"                      /usr/local/sbin/weknora-firstboot.sh

# 把 systemd 单元里的 docker 路径模板替换为实际路径
sed -i "s|@DOCKER_BIN@|${DOCKER_BIN}|g" /etc/systemd/system/weknora.service

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
