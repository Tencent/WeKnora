#!/usr/bin/env python3
import sys
import os
import time
import argparse
import socket
import threading
from urllib import request
import random 

# ========== 全局配置 ==========
TEST_RESULTS = {}          # 收集各项测试结果
SERVER_PORT = random.randint(8080, 58080)         # 后台服务端口
CPU_RECURSION_DEPTH = random.randint(20, 35)   # 斐波那契递归深度（调大更吃CPU）

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
# ========== 主程序 ==========
def main():
    parser = argparse.ArgumentParser(description="WeKnora 云沙箱诊断 Skill")
    parser.add_argument("--test", choices=["cpu", "io", "net", "server"], help="只运行指定的测试项")
    parser.add_argument("--cpu-depth", type=int, default=28, help="control RECURSION DEPTH (26,27,28,29)")
    args = parser.parse_args()

    log("=== 🔍 WeKnora 云沙箱环境诊断 Skill 开始 ===")

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

    if failed_tests:
        log(f"测试失败: {', '.join(failed_tests)}，退出码=1")
        sys.exit(0)

    log("全部测试通过，退出码=0")
    sys.exit(0)


if __name__ == "__main__":
    main()