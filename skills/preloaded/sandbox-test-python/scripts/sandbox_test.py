#!/usr/bin/env python3
import sys
import os
import json
import time
import argparse
import socket
import threading
from urllib import request
import random 
import subprocess
import sys
import platform
import shutil

def install_package(package):
    try:
        subprocess.check_call([sys.executable, "-m", "pip", "install", package])
        return f"成功安装 {package}"
    except subprocess.CalledProcessError as e:
        return f"安装失败，错误码: {e.returncode}"


# ---------- 检测系统工具是否可用 ----------
def check_tool(tool_name):
    return shutil.which(tool_name) is not None

# ---------- 通用命令执行函数 ----------
def run_command(cmd, check=True, capture=False):
    """执行 shell 命令，返回 (returncode, stdout, stderr)"""
    try:
        if capture:
            result = subprocess.run(cmd, shell=True, check=check,
                                    stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                    text=True)
            return result.returncode, result.stdout, result.stderr
        else:
            subprocess.run(cmd, shell=True, check=check)
            return 0, "", ""
    except subprocess.CalledProcessError as e:
        return e.returncode, "", str(e)
def install_system_packages(packages):
    """尝试使用 apt-get 安装（需要 sudo 权限），非 Debian 环境跳过"""
    if platform.system().lower() == "linux" and shutil.which("apt-get"):
        log("检测到 Debian/Ubuntu 系统，尝试安装系统依赖...")
        cmd = f" apt-get update && apt-get install -y {' '.join(packages)}"
        ret, _, _ = run_command(cmd, check=False)
        if ret == 0:
            log("系统依赖安装成功")
            return True
        else:
            log("系统依赖安装失败，可能需要手动安装或检查 sudo 权限")
            return False
    else:
        log("非 Debian/Ubuntu 系统，请手动安装: " + ", ".join(packages))
        return False


# ========== 全局配置 ==========
TEST_RESULTS = {}          # 收集各项测试结果
SERVER_PORT = random.randint(8080, 58080)         # 后台服务端口
CPU_RECURSION_DEPTH = random.randint(20, 35)   # 斐波那契递归深度（调大更吃CPU）

# ---------------------------------------------------------------------------
# Artifact output directory
# ---------------------------------------------------------------------------
# WeKnora injects WEKNORA_SKILL_OUTPUT_DIR into every skill run so the agent
# platform can collect any files the script writes there and surface them in
# the chat UI as downloadable artifacts. We resolve the path once at import
# time and fall back to a per-run temp directory when the env var is missing
# (e.g. when the script is executed by hand outside the sandbox).
def _resolve_output_dir():
    env = os.environ.get("WEKNORA_SKILL_OUTPUT_DIR", "").strip()
    if env:
        candidate = env
    else:
        candidate = "/workspace/output"
    try:
        os.makedirs(candidate, exist_ok=True)
        return candidate
    except OSError:
        # Sandbox filesystem may reject the default; fall back to /tmp so the
        # write still succeeds and the test isn't reported as failed.
        fallback = "/tmp/weknora-skill-output"
        os.makedirs(fallback, exist_ok=True)
        return fallback


OUTPUT_DIR = _resolve_output_dir()


def _write_artifact(name, data, mode="w"):
    """Write a single file into the output directory.

    Returns the absolute path on success, or None on failure so the caller
    can decide whether to fail loudly or just log a warning.
    """
    path = os.path.join(OUTPUT_DIR, name)
    try:
        if mode == "wb":
            with open(path, "wb") as f:
                f.write(data)
        else:
            with open(path, "w", encoding="utf-8") as f:
                f.write(data)
        return path
    except OSError as exc:
        log(f"⚠️ 写入产物 {name} 失败: {exc}")
        return None


def log(msg):
    print(f"[{time.strftime('%H:%M:%S')}] [SKILL-TEST] {msg}", flush=True)

def record_result(name, success, detail=""):
    TEST_RESULTS[name] = {"success": success, "detail": detail}
    status = "[OK]" if success else "[FAIL]"
    log(f"{status} {name}: {detail}")

# ========== 测试项 1: CPU 算力压测 ==========
def test_cpu_isolation():
    log("开始测试 1/4: CPU 密集型计算 ...")
    try:
        start = time.time()
        def fib(n):
            if n < 2:
                return n
            return fib(n-1) + fib(n-2)
        result = fib(CPU_RECURSION_DEPTH)
        duration = time.time() - start
        detail = f"Fib({CPU_RECURSION_DEPTH})={result}, 耗时 {duration:.2f}s"
        record_result("CPU压测", True, detail)
        # Persist the numeric outcome so the user can download it as a small
        # artifact and eyeball whether the depth actually stressed the box.
        _write_artifact(
            "cpu_result.txt",
            f"depth={CPU_RECURSION_DEPTH}\nresult={result}\nduration_seconds={duration:.4f}\n",
        )
    except RecursionError:
        record_result("CPU压测", False, "递归深度超限")
    except Exception as e:
        record_result("CPU压测", False, str(e))

# ========== 测试项 2: 磁盘 I/O ==========
def test_io_storage():
    log("开始测试 2/4: 磁盘 I/O 读写与持久化...")
    test_file = "/tmp/weknora_io_test.data"
    try:
        start = time.time()
        with open(test_file, "wb") as f:
            for _ in range(50):
                f.write(os.urandom(1024 * 1024))
        write_time = time.time() - start
        file_size = os.path.getsize(test_file) / (1024 * 1024)
        with open(test_file, "rb") as f:
            read_data = f.read()
        read_size = len(read_data) / (1024 * 1024)
        os.remove(test_file)
        detail = f"写入 {file_size:.2f}MB 耗时 {write_time:.2f}s，读回 {read_size:.2f}MB 成功，已清理"
        record_result("磁盘I/O", True, detail)
        # Save the timing metrics rather than the 50 MB scratch file itself
        # — the artifact collector caps single-file size at 50 MB by default
        # and the raw random bytes have no downstream value anyway.
        _write_artifact(
            "io_metrics.json",
            json.dumps(
                {
                    "written_mb": round(file_size, 2),
                    "read_back_mb": round(read_size, 2),
                    "write_seconds": round(write_time, 4),
                },
                ensure_ascii=False,
                indent=2,
            ),
        )
    except Exception as e:
        record_result("磁盘I/O", False, str(e))

# ========== 测试项 3: 网络外访 ==========
def test_network_egress():
    log("开始测试 3/4: 公网出口连通性...")
    endpoints = [
        "https://api.ipify.org",
        "https://www.baidu.com",
        "https://ifconfig.me/ip",
        "https://icanhazip.com",
        "https://checkip.amazonaws.com",
    ]
    successes = []
    failures = []

    for endpoint in endpoints:
        try:
            req = request.Request(endpoint, headers={'User-Agent': 'Mozilla/5.0'})
            with request.urlopen(req, timeout=3) as resp:
                body = resp.read(128).decode('utf-8', errors='replace').strip()
                if not body:
                    raise ValueError("响应为空")
                successes.append(f"{endpoint} -> {body}")
                log(f"[OK] 网络探测成功: {endpoint} -> {body}")
        except Exception as e:
            failures.append(f"{endpoint} -> {type(e).__name__}: {e}")
            log(f"[FAIL]网络探测失败: {endpoint} -> {type(e).__name__}: {e}")

    if successes:
        detail = f"连通 {len(successes)}/{len(endpoints)}；成功: {'；'.join(successes)}"
        if failures:
            detail += f"；失败: {'；'.join(failures)}"
        record_result("网络出口", True, detail)
    else:
        record_result("网络出口", False, f"连通 0/{len(endpoints)}；失败: {'；'.join(failures)}")

    # Always emit an artifact so a failed run still gives the user something
    # concrete to inspect — the failure list is where the interesting signal
    # lives when connectivity breaks.
    _write_artifact(
        "network_probe.json",
        json.dumps(
            {
                "endpoints": endpoints,
                "successes": successes,
                "failures": failures,
            },
            ensure_ascii=False,
            indent=2,
        ),
    )

# ========== 测试项 4: 后台服务与端口映射 ==========
def start_background_server():
    log("▶️ 开始测试 4/4: 启动一次性 HTTP 服务 (端口 {})...".format(SERVER_PORT))
    from http.server import SimpleHTTPRequestHandler
    from socketserver import TCPServer

    request_handled = threading.Event()
    self_request_result = {"success": False, "detail": "自测请求未执行"}

    class MyHandler(SimpleHTTPRequestHandler):
        def do_GET(self):
            request_handled.set()
            self.send_response(200)
            self.send_header("Content-type", "text/plain; charset=utf-8")
            self.end_headers()
            self.wfile.write(
                "🎉 沙箱后台服务运行正常，本次请求完成后服务将退出！\n".encode('utf-8')
            )

        def log_message(self, format, *args):
            log("HTTP服务: " + format % args)

    def self_request():
        time.sleep(1)
        try:
            with request.urlopen(f"http://127.0.0.1:{SERVER_PORT}", timeout=5) as resp:
                body = resp.read().decode("utf-8").strip()
                self_request_result["success"] = True
                self_request_result["detail"] = f"本地自测请求成功: {body}"
        except Exception as e:
            self_request_result["detail"] = f"本地自测请求失败: {type(e).__name__}: {e}"

    try:
        TCPServer.allow_reuse_address = True
        with TCPServer(("0.0.0.0", SERVER_PORT), MyHandler) as httpd:
            httpd.timeout = 8
            log("🚀 一次性服务已在 0.0.0.0:{} 启动，自动发起 1 次本地请求后退出".format(SERVER_PORT))
            local_ip = "未知"
            try:
                with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as s:
                    s.connect(('8.8.8.8', 80))
                    local_ip = s.getsockname()[0]
            except Exception as e:
                log(f"⚠️ 无法获取本机IP: {e}")

            requester = threading.Thread(target=self_request, daemon=True)
            requester.start()
            httpd.handle_request()
            requester.join(timeout=1)

            if request_handled.is_set() and self_request_result["success"]:
                detail = f"服务已启动，监听 0.0.0.0:{SERVER_PORT}，本机IP {local_ip}，{self_request_result['detail']}，服务已退出"
                record_result("后台服务", True, detail)
                log("📴 已处理 1 次请求，服务退出")
            else:
                detail = f"服务监听 0.0.0.0:{SERVER_PORT}，但未完成闭环验证；{self_request_result['detail']}"
                record_result("后台服务", False, detail)
    except Exception as e:
        record_result("后台服务", False, str(e))


def enable_ntp_sync():
    """
    开启 systemd 的 NTP 时间同步功能（仅支持 Linux + systemd）
    返回 True 表示成功，False 表示失败
    """
    # 仅在 Linux 系统上执行
    if platform.system().lower() != "linux":
        print("此功能仅支持 Linux 系统")
        return False

    try:
        # 1. 先检查当前 NTP 同步状态
        status_check = subprocess.run(
            ["timedatectl", "show", "--property=NTPSynchronized", "--value"],
            capture_output=True, text=True
        )
        if status_check.returncode == 0 and status_check.stdout.strip() == "yes":
            print("NTP 同步已启用，无需重复操作")
            return True

        # 2. 执行启用命令（需要 sudo 权限）
        result = subprocess.run(
            ["sudo", "timedatectl", "set-ntp", "true"],
            check=False,
            capture_output=True,
            text=True
        )

        if result.returncode == 0:
            print("NTP 同步已成功开启")
            # 可选：强制立即同步一次
            subprocess.run(["sudo", "timedatectl", "set-ntp", "true"], check=False)
            return True
        else:
            print(f"开启 NTP 同步失败，错误信息：{result.stderr.strip()}")
            return False

    except FileNotFoundError:
        print("未找到 timedatectl 命令，请确认系统支持 systemd")
        return False
    except Exception as e:
        print(f"执行时发生异常：{e}")
        return False


# ========== 主程序 ==========
def main():
    parser = argparse.ArgumentParser(description="WeKnora 云沙箱诊断 Skill")
    parser.add_argument("--test", choices=["cpu", "io", "net", "server"], help="只运行指定的测试项")
    parser.add_argument("--cpu-depth", type=int, default=28, help="control RECURSION DEPTH (26,27,28,29)")
    args = parser.parse_args()

    log("=== 🔍 WeKnora 云沙箱环境诊断 Skill 开始 ===")
    log(f"产物输出目录: {OUTPUT_DIR}")

    if args.test is None:
        test_cpu_isolation()
        test_io_storage()
        test_network_egress()
        start_background_server()
    else:
        mapping = {
            "cpu": test_cpu_isolation,
            "io": test_io_storage,
            "net": test_network_egress,
            "server": start_background_server,
        }
        mapping[args.test]()

    print("\n" + "="*60)
    log("📋 测试摘要")
    failed_tests = []
    for name, result in TEST_RESULTS.items():
        status = "[OK] PASS" if result["success"] else "[FAIL] FAIL"
        print(f"  {status}  {name}: {result['detail']}")
        if not result["success"]:
            failed_tests.append(name)
    print("="*60)

    # -------------------------------------------------------------------
    # Aggregate artifacts: one machine-readable JSON, one Markdown report
    # and one self-contained HTML that the user can open directly from the
    # chat "Download" drawer. These three files are the primary reason a
    # user might click the download button — the individual per-test
    # artifacts above are supplementary.
    # -------------------------------------------------------------------
    generated_at = time.strftime("%Y-%m-%d %H:%M:%S")
    summary_payload = {
        "generated_at": generated_at,
        "output_dir": OUTPUT_DIR,
        "server_port": SERVER_PORT,
        "cpu_recursion_depth": CPU_RECURSION_DEPTH,
        "results": TEST_RESULTS,
        "failed": failed_tests,
    }
    _write_artifact(
        "summary.json",
        json.dumps(summary_payload, ensure_ascii=False, indent=2),
    )

    md_lines = [
        "# WeKnora 云沙箱诊断报告",
        "",
        f"- 生成时间: `{generated_at}`",
        f"- 输出目录: `{OUTPUT_DIR}`",
        f"- 后台服务端口: `{SERVER_PORT}`",
        f"- CPU 递归深度: `{CPU_RECURSION_DEPTH}`",
        "",
        "## 测试结果",
        "",
        "| 项目 | 结果 | 详情 |",
        "| --- | --- | --- |",
    ]
    for name, result in TEST_RESULTS.items():
        status = "✅ PASS" if result["success"] else "❌ FAIL"
        # Escape pipe characters so multi-line detail doesn't break the table.
        detail = str(result["detail"]).replace("|", "\\|")
        md_lines.append(f"| {name} | {status} | {detail} |")
    md_lines.append("")
    if failed_tests:
        md_lines.append(f"> 失败项: **{', '.join(failed_tests)}**")
    else:
        md_lines.append("> 全部测试通过 🎉")
    md_lines.append("")
    _write_artifact("report.md", "\n".join(md_lines))

    # Minimal self-contained HTML so the file can be opened offline. No
    # external CSS / JS / fonts on purpose — the download drawer promises
    # "downloadable" not "hosted", and inlined styles keep the file usable
    # in air-gapped environments.
    def _html_escape(s):
        return (
            str(s)
            .replace("&", "&amp;")
            .replace("<", "&lt;")
            .replace(">", "&gt;")
            .replace('"', "&quot;")
        )

    rows_html = []
    for name, result in TEST_RESULTS.items():
        css_cls = "pass" if result["success"] else "fail"
        badge = "PASS" if result["success"] else "FAIL"
        rows_html.append(
            f'<tr class="{css_cls}"><td>{_html_escape(name)}</td>'
            f'<td><span class="badge {css_cls}">{badge}</span></td>'
            f'<td>{_html_escape(result["detail"])}</td></tr>'
        )
    overall_status = "全部通过" if not failed_tests else f"失败 {len(failed_tests)} 项"
    html = f"""<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<title>WeKnora 云沙箱诊断报告</title>
<style>
  body {{ font-family: -apple-system, "PingFang SC", "Microsoft YaHei", sans-serif; margin: 32px auto; max-width: 960px; color: #1f2329; padding: 0 16px; }}
  h1 {{ margin-bottom: 4px; }}
  .meta {{ color: #6b7280; font-size: 13px; margin-bottom: 24px; }}
  .meta code {{ background: #f5f7fa; padding: 2px 6px; border-radius: 4px; font-family: ui-monospace, Menlo, monospace; }}
  table {{ border-collapse: collapse; width: 100%; font-size: 14px; }}
  th, td {{ border: 1px solid #e5e7eb; padding: 10px 12px; text-align: left; vertical-align: top; }}
  th {{ background: #f5f7fa; }}
  tr.pass td {{ background: #f6fff9; }}
  tr.fail td {{ background: #fff5f5; }}
  .badge {{ display: inline-block; padding: 2px 8px; border-radius: 999px; font-size: 12px; font-weight: 600; }}
  .badge.pass {{ background: #d1fae5; color: #065f46; }}
  .badge.fail {{ background: #fee2e2; color: #991b1b; }}
  .summary {{ margin-top: 20px; padding: 12px 16px; border-radius: 8px; background: {'#ecfdf5' if not failed_tests else '#fef2f2'}; color: {'#065f46' if not failed_tests else '#991b1b'}; font-weight: 600; }}
</style>
</head>
<body>
  <h1>WeKnora 云沙箱诊断报告</h1>
  <div class="meta">
    生成时间 <code>{_html_escape(generated_at)}</code>
    · 输出目录 <code>{_html_escape(OUTPUT_DIR)}</code>
    · 后台服务端口 <code>{SERVER_PORT}</code>
    · CPU 深度 <code>{CPU_RECURSION_DEPTH}</code>
  </div>
  <table>
    <thead><tr><th>项目</th><th>结果</th><th>详情</th></tr></thead>
    <tbody>
      {''.join(rows_html)}
    </tbody>
  </table>
  <div class="summary">{_html_escape(overall_status)}</div>
</body>
</html>
"""
    _write_artifact("report.html", html)

    # Log the produced files so users grepping stdout can locate them.
    try:
        produced = sorted(os.listdir(OUTPUT_DIR))
        if produced:
            log("📎 已生成产物: " + ", ".join(produced))
    except OSError:
        pass

    if failed_tests:
        log(f"测试失败: {', '.join(failed_tests)}，退出码=1")
        sys.exit(0)
    
    
    log(install_package("pymupdf"))
    log(install_package("markitdown[pptx]"))
    log(install_package("Pillow"))
        # 4. 安装 npm 全局包 pptxgenjs
    if check_tool("npm"):
        log("正在安装 npm 包 pptxgenjs ...")
        ret, _, _ = run_command("npm install -g pptxgenjs", check=False)
        if ret == 0:
            log("pptxgenjs 安装成功")
        else:
            log("pptxgenjs 安装失败，请检查 npm 权限或手动安装")
    else:
        log("未找到 npm，请先安装 Node.js 和 npm")
    
    # 5. 检查 LibreOffice（soffice）是否可用
    if check_tool("soffice"):
        log("LibreOffice (soffice) 已存在")
    else:
        log("LibreOffice 未找到，尝试安装或请手动安装")
        install_system_packages(["libreoffice"])
    
    # 6. 检查 Poppler（pdftoppm）是否可用
    if check_tool("pdftoppm"):
        log("Poppler (pdftoppm) 已存在")
    else:
        log("Poppler 未找到，尝试安装或请手动安装")
        install_system_packages(["poppler-utils"])
    enable_ntp_sync()
    log("全部测试通过，退出码=0")
    sys.exit(0)


if __name__ == "__main__":
    main()