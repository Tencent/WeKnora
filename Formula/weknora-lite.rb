class WeknoraLite < Formula
  desc "Knowledge base management system — single-binary Lite edition"
  homepage "https://github.com/Tencent/WeKnora"
  version "0.0.0"
  license "Apache-2.0"

  on_macos do
    on_arm do
      url "https://github.com/Tencent/WeKnora/releases/download/v#{version}/WeKnora-lite_v#{version}_darwin_arm64.tar.gz"
      sha256 "PLACEHOLDER"
    end
    on_intel do
      url "https://github.com/Tencent/WeKnora/releases/download/v#{version}/WeKnora-lite_v#{version}_darwin_amd64.tar.gz"
      sha256 "PLACEHOLDER"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/Tencent/WeKnora/releases/download/v#{version}/WeKnora-lite_v#{version}_linux_arm64.tar.gz"
      sha256 "PLACEHOLDER"
    end
    on_intel do
      url "https://github.com/Tencent/WeKnora/releases/download/v#{version}/WeKnora-lite_v#{version}_linux_amd64.tar.gz"
      sha256 "PLACEHOLDER"
    end
  end

  def install
    libexec.install "WeKnora-lite"
    pkgshare.install "web" if File.directory?("web")
    pkgshare.install "config" if File.directory?("config")
    pkgshare.install ".env.lite.example"
    doc.install "README.md"
    pkgshare.install "migrations" if File.directory?("migrations")

    (bin/"weknora-lite").write <<~SH
      #!/bin/bash
      CONFIG_DIR="${WEKNORA_CONFIG_DIR:-${XDG_CONFIG_HOME:-$HOME/.config}/weknora}"
      DATA_DIR="${WEKNORA_DATA_DIR:-${XDG_DATA_HOME:-$HOME/.local/share}/weknora}"

      mkdir -p "$DATA_DIR/files" "$CONFIG_DIR/config" 2>/dev/null

      if [ ! -f "$CONFIG_DIR/config/config.yaml" ]; then
        cp -r "#{pkgshare}/config/" "$CONFIG_DIR/config/"
      fi

      if [ ! -d "$CONFIG_DIR/migrations" ] && [ -d "#{pkgshare}/migrations" ]; then
        ln -sf "#{pkgshare}/migrations" "$CONFIG_DIR/migrations"
      fi

      if [ ! -f "$CONFIG_DIR/.env.lite" ]; then
        cp "#{pkgshare}/.env.lite.example" "$CONFIG_DIR/.env.lite"
        sed -i '' "s|DB_PATH=.*|DB_PATH=$DATA_DIR/weknora.db|" "$CONFIG_DIR/.env.lite"
        sed -i '' "s|LOCAL_STORAGE_BASE_DIR=.*|LOCAL_STORAGE_BASE_DIR=$DATA_DIR/files|" "$CONFIG_DIR/.env.lite"
        rm -f "$CONFIG_DIR/.env.lite-e"
        echo ""
        echo "已创建配置文件: $CONFIG_DIR/.env.lite"
        echo "请根据需要编辑（如修改 LLM 地址、安全密钥等）。"
        echo ""
      fi

      set -a
      source "$CONFIG_DIR/.env.lite"
      set +a

      export DB_PATH="${DB_PATH:-$DATA_DIR/weknora.db}"
      export LOCAL_STORAGE_BASE_DIR="${LOCAL_STORAGE_BASE_DIR:-$DATA_DIR/files}"
      export WEKNORA_WEB_DIR="${WEKNORA_WEB_DIR:-#{pkgshare}/web}"

      cd "$CONFIG_DIR"
      exec "#{libexec}/WeKnora-lite" "$@"
    SH
  end

  def post_install
    (var/"weknora").mkpath
    (var/"log").mkpath
  end

  service do
    run [bin/"weknora-lite"]
    keep_alive true
    working_dir var/"weknora"
    log_path var/"log/weknora-lite.log"
    error_log_path var/"log/weknora-lite.log"
  end

  def caveats
    <<~EOS
      前台运行:
        weknora-lite

      后台服务（推荐）:
        brew services start weknora-lite   # 启动并开机自启
        brew services stop weknora-lite    # 停止
        brew services restart weknora-lite # 重启
        brew services info weknora-lite    # 查看状态

      日志:
        #{var}/log/weknora-lite.log

      首次运行会自动创建配置文件:
        ~/.config/weknora/.env.lite

      数据存储在:
        ~/.local/share/weknora/

      如需修改配置（LLM 服务地址、安全密钥等）:
        $EDITOR ~/.config/weknora/.env.lite
        brew services restart weknora-lite
    EOS
  end

  test do
    assert_predicate bin/"weknora-lite", :executable?
  end
end
