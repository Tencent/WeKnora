"""gRPC TLS 和认证模块

环境变量配置：
    TLS 相关：
        GRPC_TLS_ENABLED: 是否启用 TLS（true/false），默认 false
        GRPC_TLS_CERT: TLS 证书文件路径（GRPC_TLS_ENABLED=true 时必填）
        GRPC_TLS_KEY: TLS 私钥文件路径（GRPC_TLS_ENABLED=true 时必填）
        GRPC_TLS_CA: CA 证书路径
        GRPC_MTLS_REQUIRE_CLIENT_CERT: 设为 true 时启用 mTLS，要求客户端
            出示由 GRPC_TLS_CA 签发的证书。未设置时默认按 GRPC_TLS_CA 是否
            存在自动判断（保留向后兼容）。

    认证相关：
        GRPC_AUTH_TOKEN: 认证 Token，如果设置则启用认证

注意：当 GRPC_TLS_ENABLED=true 但任何 TLS 配置项缺失或加载失败时，
本模块会抛出异常以触发 fail-fast，避免静默降级到明文。
"""

import hmac
import logging
import os
from typing import Optional

import grpc

logger = logging.getLogger(__name__)


class TLSConfigError(RuntimeError):
    """TLS 配置错误，用于 fail-fast。"""


def _env_bool(name: str, default: bool = False) -> bool:
    raw = os.getenv(name)
    if raw is None:
        return default
    return raw.strip().lower() in ("true", "1", "yes", "on")


def load_tls_credentials() -> Optional[grpc.ServerCredentials]:
    """构建 server 端 TLS 凭据。

    GRPC_TLS_ENABLED=false 时返回 None；为 true 时如果配置无效会抛出
    TLSConfigError，由调用方决定是否终止启动。
    """
    if not _env_bool("GRPC_TLS_ENABLED", False):
        logger.info("TLS disabled (GRPC_TLS_ENABLED is not 'true')")
        return None

    cert_path = os.getenv("GRPC_TLS_CERT")
    key_path = os.getenv("GRPC_TLS_KEY")

    if not cert_path or not key_path:
        raise TLSConfigError(
            "GRPC_TLS_ENABLED=true but GRPC_TLS_CERT/GRPC_TLS_KEY not set; "
            "refusing to start in plaintext mode"
        )

    try:
        with open(cert_path, "rb") as f:
            cert_chain = f.read()
        with open(key_path, "rb") as f:
            private_key = f.read()
    except OSError as e:
        raise TLSConfigError(f"failed to read TLS cert/key: {e}") from e

    ca_path = os.getenv("GRPC_TLS_CA")
    require_client_auth = _env_bool(
        "GRPC_MTLS_REQUIRE_CLIENT_CERT",
        default=bool(ca_path),
    )

    if require_client_auth and not ca_path:
        raise TLSConfigError(
            "GRPC_MTLS_REQUIRE_CLIENT_CERT=true requires GRPC_TLS_CA to be set"
        )

    if ca_path:
        try:
            with open(ca_path, "rb") as f:
                ca_cert = f.read()
        except OSError as e:
            raise TLSConfigError(f"failed to read CA cert: {e}") from e
        credentials = grpc.ssl_server_credentials(
            [(private_key, cert_chain)],
            root_certificates=ca_cert,
            require_client_auth=require_client_auth,
        )
        if require_client_auth:
            logger.info("TLS enabled with mTLS (mutual authentication)")
        else:
            logger.info("TLS enabled with CA configured (client auth optional)")
    else:
        credentials = grpc.ssl_server_credentials([(private_key, cert_chain)])
        logger.info("TLS enabled (1-way)")

    return credentials


# gRPC 健康检查标准服务路径，需在鉴权前放行，便于 K8s/Docker 探活。
_HEALTH_METHODS = frozenset(
    {
        "/grpc.health.v1.Health/Check",
        "/grpc.health.v1.Health/Watch",
    }
)


def _make_abort_handler(original: Optional[grpc.RpcMethodHandler]) -> grpc.RpcMethodHandler:
    """为给定的原 handler 构造一个匹配 RPC kind 的鉴权失败 handler。

    如果原 handler 不可用（理论上不会发生，但作为兜底），返回 unary_unary。
    直接返回 None 而不是 set_code 也可，但显式调用 abort 能确保框架按
    UNAUTHENTICATED 收尾，且匹配 kind 防止 grpc 触发 INTERNAL。
    """
    def _abort(_request, context):
        context.abort(
            grpc.StatusCode.UNAUTHENTICATED,
            "Invalid or missing authentication token",
        )

    def _abort_stream(_request, context):
        context.abort(
            grpc.StatusCode.UNAUTHENTICATED,
            "Invalid or missing authentication token",
        )
        return
        yield  # pragma: no cover - make this a generator

    if original is None or original.unary_unary is not None:
        return grpc.unary_unary_rpc_method_handler(
            _abort,
            request_deserializer=getattr(original, "request_deserializer", None),
            response_serializer=getattr(original, "response_serializer", None),
        )
    if original.unary_stream is not None:
        return grpc.unary_stream_rpc_method_handler(
            _abort_stream,
            request_deserializer=original.request_deserializer,
            response_serializer=original.response_serializer,
        )
    if original.stream_unary is not None:
        return grpc.stream_unary_rpc_method_handler(
            _abort,
            request_deserializer=original.request_deserializer,
            response_serializer=original.response_serializer,
        )
    return grpc.stream_stream_rpc_method_handler(
        _abort_stream,
        request_deserializer=original.request_deserializer,
        response_serializer=original.response_serializer,
    )


class AuthInterceptor(grpc.ServerInterceptor):
    """Token 认证拦截器

    环境变量配置：
        GRPC_AUTH_TOKEN: 认证 Token，如果设置则启用认证

    客户端需要在 metadata 中传递 Token：
        - key: "authorization"
        - value: "Bearer <token>" 或直接 "<token>"
    """

    def __init__(self) -> None:
        token = os.getenv("GRPC_AUTH_TOKEN") or ""
        self.auth_token: Optional[bytes] = token.encode("utf-8") if token else None
        if self.auth_token:
            if len(self.auth_token) < 16:
                logger.warning(
                    "GRPC_AUTH_TOKEN is shorter than 16 bytes; consider a stronger token"
                )
            logger.info("Token authentication enabled")
        else:
            logger.warning("Token authentication disabled (GRPC_AUTH_TOKEN not set)")

    def intercept_service(self, continuation, handler_call_details):
        if not self.auth_token:
            return continuation(handler_call_details)

        method = handler_call_details.method
        if method in _HEALTH_METHODS:
            return continuation(handler_call_details)

        metadata = dict(handler_call_details.invocation_metadata or [])
        raw = metadata.get("authorization", "") or ""
        if raw.startswith("Bearer "):
            raw = raw[7:]
        token_bytes = raw.encode("utf-8")

        if not hmac.compare_digest(token_bytes, self.auth_token):
            logger.warning("Authentication failed for method: %s", method)
            original = continuation(handler_call_details)
            return _make_abort_handler(original)

        return continuation(handler_call_details)
