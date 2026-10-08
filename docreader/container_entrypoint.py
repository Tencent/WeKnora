"""Keep ARM virtual machines with incomplete CPU support from crashing at boot.

Probe in a child: SIGILL cannot be caught as a Python exception. Only that
specific failure permits the OpenSSL portable path; other failures stay fatal.
OPENSSL_armcap is documented at https://docs.openssl.org/master/man3/OPENSSL_armcap/
"""

import os
import platform
import signal
import subprocess
import sys


PROBE = """
import resource
resource.setrlimit(resource.RLIMIT_CORE, (0, 0))
from cryptography.hazmat.primitives.ciphers.aead import AESGCM
cipher = AESGCM(bytes(32))
nonce = bytes(12)
encrypted = cipher.encrypt(nonce, b'docreader startup probe', b'probe')
assert cipher.decrypt(nonce, encrypted, b'probe') == b'docreader startup probe'
"""


def runtime_environment():
    env = os.environ.copy()
    if platform.machine().lower() not in {"arm64", "aarch64"} or "OPENSSL_armcap" in env:
        return env
    command = [sys.executable, "-c", PROBE]
    result = subprocess.run(command, env=env, capture_output=True, timeout=30)
    if result.returncode == -signal.SIGILL:
        env["OPENSSL_armcap"] = "0"
        result = subprocess.run(command, env=env, capture_output=True, timeout=30)
        if result.returncode == 0:
            print("DocReader: ARM OpenSSL probe raised SIGILL; using portable CPU implementations (OPENSSL_armcap=0).", file=sys.stderr, flush=True)
    if result.returncode != 0:
        raise RuntimeError(f"DocReader crypto startup probe failed ({result.returncode}): {result.stderr.decode(errors='replace')}")
    return env


def main():
    if len(sys.argv) < 2:
        raise SystemExit("DocReader entrypoint requires a command")
    os.execvpe(sys.argv[1], sys.argv[1:], runtime_environment())


if __name__ == "__main__":
    main()
