"""gRPC TLS 和认证模块

环境变量配置：
    TLS 相关：
        GRPC_TLS_ENABLED: 是否启用 TLS（true/false），默认 false
        GRPC_TLS_CERT: TLS 证书文件路径
        GRPC_TLS_KEY: TLS 私钥文件路径
        GRPC_TLS_CA: CA 证书路径（可选，用于 mTLS 双向认证）

    认证相关：
        GRPC_AUTH_TOKEN: 认证 Token，如果设置则启用认证
"""

import logging
import os
from typing import Optional

import grpc

logger = logging.getLogger(__name__)


def load_tls_credentials() -> Optional[grpc.ServerCredentials]:
    tls_enabled = os.getenv("GRPC_TLS_ENABLED", "false").lower() == "true"
    if not tls_enabled:
        logger.info("TLS disabled (GRPC_TLS_ENABLED is not 'true')")
        return None

    cert_path = os.getenv("GRPC_TLS_CERT")
    key_path = os.getenv("GRPC_TLS_KEY")

    if not cert_path or not key_path:
        logger.warning(
            "TLS enabled but certificate not configured (GRPC_TLS_CERT or GRPC_TLS_KEY not set)"
        )
        return None

    try:
        with open(cert_path, "rb") as f:
            cert_chain = f.read()
        with open(key_path, "rb") as f:
            private_key = f.read()

        ca_path = os.getenv("GRPC_TLS_CA")
        if ca_path:
            with open(ca_path, "rb") as f:
                ca_cert = f.read()
            credentials = grpc.ssl_server_credentials(
                [(private_key, cert_chain)],
                root_certificates=ca_cert,
                require_client_auth=True,
            )
            logger.info("TLS enabled with mTLS (mutual authentication)")
        else:
            credentials = grpc.ssl_server_credentials([(private_key, cert_chain)])
            logger.info("TLS enabled")

        return credentials
    except FileNotFoundError as e:
        logger.error(f"TLS certificate file not found: {e}")
        return None
    except Exception as e:
        logger.error(f"Failed to load TLS credentials: {e}")
        return None


class AuthInterceptor(grpc.ServerInterceptor):
    """Token 认证拦截器

    环境变量配置：
        GRPC_AUTH_TOKEN: 认证 Token，如果设置则启用认证

    客户端需要在 metadata 中传递 Token：
        - key: "authorization"
        - value: "Bearer <token>" 或直接 "<token>"
    """

    def __init__(self):
        self.auth_token = os.getenv("GRPC_AUTH_TOKEN")
        if self.auth_token:
            logger.info("Token authentication enabled")
        else:
            logger.warning("Token authentication disabled (GRPC_AUTH_TOKEN not set)")

    def intercept_service(self, continuation, handler_call_details):
        if not self.auth_token:
            return continuation(handler_call_details)

        method = handler_call_details.method
        if method.endswith("/Check") or method.endswith("/Watch"):
            return continuation(handler_call_details)

        metadata = dict(handler_call_details.invocation_metadata or [])
        token = metadata.get("authorization", "")

        if token.startswith("Bearer "):
            token = token[7:]

        if token != self.auth_token:
            logger.warning(f"Authentication failed for method: {method}")
            return self._unauthenticated_handler()

        return continuation(handler_call_details)

    def _unauthenticated_handler(self):
        def handler(request, context):
            context.set_code(grpc.StatusCode.UNAUTHENTICATED)
            context.set_details("Invalid or missing authentication token")
            return None

        return grpc.unary_unary_rpc_method_handler(handler)
