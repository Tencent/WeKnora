# -*- coding: utf-8 -*-
import io
import logging
import os
import traceback
import uuid
from abc import ABC, abstractmethod
from typing import Dict, Optional

from minio import Minio
from qcloud_cos import CosConfig, CosS3Client

from docreader.utils import endecode

logger = logging.getLogger(__name__)


def _cfg(storage_config: Optional[Dict], key: str, *env_keys: str, default: str = "") -> str:
    """Read a value from storage_config dict, falling back to env vars."""
    if storage_config:
        v = storage_config.get(key, "")
        if v:
            return str(v)
    for ek in env_keys:
        v = os.environ.get(ek, "")
        if v:
            return v
    return default


class Storage(ABC):
    """Abstract base class for object storage operations"""

    @abstractmethod
    def upload_file(self, file_path: str) -> str:
        pass

    @abstractmethod
    def upload_bytes(self, content: bytes, file_ext: str = ".png") -> str:
        pass


class CosStorage(Storage):
    """Tencent Cloud COS storage implementation"""

    def __init__(self, storage_config: Optional[Dict] = None):
        self.storage_config = storage_config
        self.client, self.bucket_name, self.region, self.prefix = (
            self._init_cos_client()
        )

    def _init_cos_client(self):
        try:
            sc = self.storage_config
            secret_id = _cfg(sc, "access_key_id", "COS_SECRET_ID")
            secret_key = _cfg(sc, "secret_access_key", "COS_SECRET_KEY")
            region = _cfg(sc, "region", "COS_REGION")
            bucket_name = _cfg(sc, "bucket_name", "COS_BUCKET_NAME")
            appid = _cfg(sc, "app_id", "COS_APP_ID")
            prefix = _cfg(sc, "path_prefix", "COS_PATH_PREFIX")
            enable_old_domain = os.environ.get("COS_ENABLE_OLD_DOMAIN", "").lower() in ("1", "true", "yes")

            if not all([secret_id, secret_key, region, bucket_name, appid]):
                logger.error(
                    "Incomplete COS configuration: "
                    "secret_id=%s, region=%s, bucket=%s, appid=%s",
                    bool(secret_id), region, bucket_name, appid,
                )
                return None, None, None, None

            logger.info("Initializing COS client: region=%s, bucket=%s", region, bucket_name)
            config = CosConfig(
                Appid=appid,
                Region=region,
                SecretId=secret_id,
                SecretKey=secret_key,
                EnableOldDomain=enable_old_domain,
            )
            client = CosS3Client(config)
            return client, bucket_name, region, prefix
        except Exception as e:
            logger.error("Failed to initialize COS client: %s", e)
            return None, None, None, None

    def _get_download_url(self, bucket_name, region, object_key):
        return f"https://{bucket_name}.cos.{region}.myqcloud.com/{object_key}"

    def upload_file(self, file_path: str) -> str:
        try:
            if not self.client:
                return ""
            file_ext = os.path.splitext(file_path)[1]
            object_key = f"{self.prefix}/images/{uuid.uuid4().hex}{file_ext}"
            self.client.upload_file(
                Bucket=self.bucket_name,
                LocalFilePath=file_path,
                Key=object_key,
            )
            file_url = self._get_download_url(self.bucket_name, self.region, object_key)
            logger.info("COS upload_file ok: %s", file_url)
            return file_url
        except Exception as e:
            logger.error("COS upload_file failed: %s", e)
            return ""

    def upload_bytes(self, content: bytes, file_ext: str = ".png") -> str:
        try:
            if not self.client:
                return ""
            object_key = (
                f"{self.prefix}/images/{uuid.uuid4().hex}{file_ext}"
                if self.prefix
                else f"images/{uuid.uuid4().hex}{file_ext}"
            )
            self.client.put_object(
                Bucket=self.bucket_name, Body=content, Key=object_key
            )
            file_url = self._get_download_url(self.bucket_name, self.region, object_key)
            logger.info("COS upload_bytes ok: %s", file_url)
            return file_url
        except Exception as e:
            logger.error("COS upload_bytes failed: %s", e)
            traceback.print_exc()
            return ""


class MinioStorage(Storage):
    """MinIO storage implementation"""

    def __init__(self, storage_config: Optional[Dict] = None):
        self.storage_config = storage_config
        self.client, self.bucket_name, self.use_ssl, self.endpoint, self.path_prefix = (
            self._init_minio_client()
        )

    def _init_minio_client(self):
        try:
            sc = self.storage_config
            access_key = _cfg(sc, "access_key_id", "MINIO_ACCESS_KEY_ID")
            secret_key = _cfg(sc, "secret_access_key", "MINIO_SECRET_ACCESS_KEY")
            bucket_name = _cfg(sc, "bucket_name", "MINIO_BUCKET_NAME")
            path_prefix_raw = _cfg(sc, "path_prefix", "MINIO_PATH_PREFIX")
            path_prefix = path_prefix_raw.strip().strip("/") if path_prefix_raw else ""
            endpoint = _cfg(sc, "endpoint", "MINIO_ENDPOINT")
            use_ssl = os.environ.get("MINIO_USE_SSL", "").lower() in ("1", "true", "yes")

            if not all([endpoint, access_key, secret_key, bucket_name]):
                logger.error("Incomplete MinIO configuration")
                return None, None, None, None, None

            client = Minio(
                endpoint, access_key=access_key, secret_key=secret_key, secure=use_ssl
            )

            found = client.bucket_exists(bucket_name)
            if not found:
                client.make_bucket(bucket_name)
                policy = (
                    "{"
                    '"Version":"2012-10-17",'
                    '"Statement":['
                    '{"Effect":"Allow","Principal":{"AWS":["*"]},'
                    '"Action":["s3:GetBucketLocation","s3:ListBucket"],'
                    '"Resource":["arn:aws:s3:::%s"]},'
                    '{"Effect":"Allow","Principal":{"AWS":["*"]},'
                    '"Action":["s3:GetObject"],'
                    '"Resource":["arn:aws:s3:::%s/*"]}'
                    "]}" % (bucket_name, bucket_name)
                )
                client.set_bucket_policy(bucket_name, policy)

            return client, bucket_name, use_ssl, endpoint, path_prefix
        except Exception as e:
            logger.error("Failed to initialize MinIO client: %s", e)
            return None, None, None, None, None

    def _get_download_url(self, object_key: str):
        public_endpoint = os.environ.get("MINIO_PUBLIC_ENDPOINT", "")
        if public_endpoint:
            return f"{public_endpoint}/{self.bucket_name}/{object_key}"
        scheme = "https" if self.use_ssl else "http"
        return f"{scheme}://{self.endpoint}/{self.bucket_name}/{object_key}"

    def upload_file(self, file_path: str) -> str:
        try:
            if not self.client:
                return ""
            file_name = os.path.basename(file_path)
            object_key = (
                f"{self.path_prefix}/images/{uuid.uuid4().hex}{os.path.splitext(file_name)[1]}"
                if self.path_prefix
                else f"images/{uuid.uuid4().hex}{os.path.splitext(file_name)[1]}"
            )
            with open(file_path, "rb") as file_data:
                file_size = os.path.getsize(file_path)
                self.client.put_object(
                    bucket_name=self.bucket_name or "",
                    object_name=object_key,
                    data=file_data,
                    length=file_size,
                    content_type="application/octet-stream",
                )
            file_url = self._get_download_url(object_key)
            logger.info("MinIO upload_file ok: %s", file_url)
            return file_url
        except Exception as e:
            logger.error("MinIO upload_file failed: %s", e)
            return ""

    def upload_bytes(self, content: bytes, file_ext: str = ".png") -> str:
        try:
            if not self.client:
                return ""
            object_key = (
                f"{self.path_prefix}/images/{uuid.uuid4().hex}{file_ext}"
                if self.path_prefix
                else f"images/{uuid.uuid4().hex}{file_ext}"
            )
            self.client.put_object(
                self.bucket_name or "",
                object_key,
                data=io.BytesIO(content),
                length=len(content),
                content_type="application/octet-stream",
            )
            file_url = self._get_download_url(object_key)
            logger.info("MinIO upload_bytes ok: %s", file_url)
            return file_url
        except Exception as e:
            logger.error("MinIO upload_bytes failed: %s", e)
            traceback.print_exc()
            return ""


class LocalStorage(Storage):
    """Local file system storage implementation.

    Saves files under base_dir and returns web-accessible URL paths
    (e.g. /files/images/uuid.jpg) so that the Go app can serve them.
    """

    def __init__(self, storage_config: Optional[Dict] = None):
        sc = storage_config or {}
        self.base_dir = (
            sc.get("base_dir")
            or os.environ.get("LOCAL_STORAGE_BASE_DIR", "/data/files")
        )
        path_prefix = (sc.get("path_prefix") or "").strip().strip("/")
        if path_prefix:
            self.image_dir = os.path.join(self.base_dir, path_prefix, "images")
        else:
            self.image_dir = os.path.join(self.base_dir, "images")
        self.url_prefix = (
            sc.get("url_prefix")
            or os.environ.get("LOCAL_STORAGE_URL_PREFIX", "/files")
        )
        os.makedirs(self.image_dir, exist_ok=True)

    def _to_url(self, fpath: str) -> str:
        if self.url_prefix:
            rel = os.path.relpath(fpath, self.base_dir)
            return f"{self.url_prefix}/{rel}"
        return fpath

    def upload_file(self, file_path: str) -> str:
        return file_path

    def upload_bytes(self, content: bytes, file_ext: str = ".png") -> str:
        fpath = os.path.join(self.image_dir, f"{uuid.uuid4()}{file_ext}")
        with open(fpath, "wb") as f:
            f.write(content)
        url = self._to_url(fpath)
        logger.info("Local storage saved: %s -> %s", fpath, url)
        return url


class Base64Storage(Storage):
    def upload_file(self, file_path: str) -> str:
        return file_path

    def upload_bytes(self, content: bytes, file_ext: str = ".png") -> str:
        file_ext = file_ext.lstrip(".")
        return f"data:image/{file_ext};base64,{endecode.decode_image(content)}"


class DummyStorage(Storage):
    """Dummy storage — all uploads return empty string."""

    def upload_file(self, file_path: str) -> str:
        return ""

    def upload_bytes(self, content: bytes, file_ext: str = ".png") -> str:
        return ""


def create_storage(storage_config: Optional[Dict[str, str]] = None) -> Storage:
    """Create a storage instance based on storage_config dict.

    The ``provider`` key in storage_config determines the backend:
      minio, cos, local, base64.
    Falls back to STORAGE_TYPE env var, then ``local``.
    """
    storage_type = ""
    if storage_config:
        provider = str(storage_config.get("provider", "")).lower().strip()
        if provider and provider not in ("unspecified", "storage_provider_unspecified"):
            storage_type = provider

    if not storage_type:
        storage_type = os.environ.get("STORAGE_TYPE", "local").lower().strip()

    logger.info("Creating %s storage instance", storage_type)

    if storage_type == "minio":
        return MinioStorage(storage_config)
    elif storage_type == "cos":
        return CosStorage(storage_config)
    elif storage_type == "local":
        return LocalStorage(storage_config)
    elif storage_type == "base64":
        return Base64Storage()
    return DummyStorage()
