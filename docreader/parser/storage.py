# -*- coding: utf-8 -*-
import io
import logging
import os
import traceback
import uuid
from abc import ABC, abstractmethod
from typing import Dict

from minio import Minio
from qcloud_cos import CosConfig, CosS3Client

from docreader.config import CONFIG
from docreader.utils import endecode

logger = logging.getLogger(__name__)


class Storage(ABC):
    """Abstract base class for object storage operations"""

    @abstractmethod
    def upload_file(self, file_path: str) -> str:
        """Upload file to object storage

        Args:
            file_path: File path

        Returns:
            File URL
        """
        pass

    @abstractmethod
    def upload_bytes(self, content: bytes, file_ext: str = ".png") -> str:
        """Upload bytes to object storage

        Args:
            content: Byte content to upload
            file_ext: File extension

        Returns:
            File URL
        """
        pass


class CosStorage(Storage):
    """Tencent Cloud COS storage implementation"""

    def __init__(self, storage_config=None):
        """Initialize COS storage

        Args:
            storage_config: Storage configuration
        """
        self.storage_config = storage_config
        self.client, self.bucket_name, self.region, self.prefix = (
            self._init_cos_client()
        )

    def _init_cos_client(self):
        """Initialize Tencent Cloud COS client"""
        try:
            # Use provided COS config if available,
            # otherwise fall back to environment variables
            if self.storage_config and self.storage_config.get("access_key_id") != "":
                cos_config = self.storage_config
                secret_id = cos_config.get("access_key_id")
                secret_key = cos_config.get("secret_access_key")
                region = cos_config.get("region")
                bucket_name = cos_config.get("bucket_name")
                appid = cos_config.get("app_id")
                prefix = cos_config.get("path_prefix", "")
            else:
                # Get COS configuration from environment variables
                secret_id = CONFIG.cos_secret_id
                secret_key = CONFIG.cos_secret_key
                region = CONFIG.cos_region
                bucket_name = CONFIG.cos_bucket_name
                appid = CONFIG.cos_app_id
                prefix = CONFIG.cos_path_prefix

            enable_old_domain = CONFIG.cos_enable_old_domain

            if not all([secret_id, secret_key, region, bucket_name, appid]):
                logger.error(
                    "Incomplete COS configuration, missing environment variables"
                    f"secret_id: {secret_id}, secret_key: {secret_key}, "
                    f"region: {region}, bucket_name: {bucket_name}, appid: {appid}"
                )
                return None, None, None, None

            # Initialize COS configuration
            logger.info(
                f"Initializing COS client with region: {region}, bucket: {bucket_name}"
            )
            config = CosConfig(
                Appid=appid,
                Region=region,
                SecretId=secret_id,
                SecretKey=secret_key,
                EnableOldDomain=enable_old_domain,
            )

            # Create client
            client = CosS3Client(config)
            return client, bucket_name, region, prefix
        except Exception as e:
            logger.error(f"Failed to initialize COS client: {str(e)}")
            return None, None, None, None

    def _get_download_url(self, bucket_name, region, object_key):
        """Generate COS object URL

        Args:
            bucket_name: Bucket name
            region: Region
            object_key: Object key

        Returns:
            File URL
        """
        return f"https://{bucket_name}.cos.{region}.myqcloud.com/{object_key}"

    def upload_file(self, file_path: str) -> str:
        """Upload file to Tencent Cloud COS

        Args:
            file_path: File path

        Returns:
            File URL
        """
        logger.info(f"Uploading file to COS: {file_path}")
        try:
            if not self.client:
                return ""

            # Generate object key, use UUID to avoid conflicts
            file_ext = os.path.splitext(file_path)[1]
            object_key = f"{self.prefix}/images/{uuid.uuid4().hex}{file_ext}"
            logger.info(f"Generated object key: {object_key}")

            # Upload file
            logger.info("Attempting to upload file to COS")
            self.client.upload_file(
                Bucket=self.bucket_name,
                LocalFilePath=file_path,
                Key=object_key,
            )

            # Get file URL
            file_url = self._get_download_url(self.bucket_name, self.region, object_key)

            logger.info(f"Successfully uploaded file to COS: {file_url}")
            return file_url

        except Exception as e:
            logger.error(f"Failed to upload file to COS: {str(e)}")
            return ""

    def upload_bytes(self, content: bytes, file_ext: str = ".png") -> str:
        """Upload bytes to Tencent Cloud COS

        Args:
            content: Byte content to upload
            file_ext: File extension

        Returns:
            File URL
        """
        try:
            logger.info(f"Uploading bytes content to COS, size: {len(content)} bytes")
            if not self.client:
                return ""

            object_key = (
                f"{self.prefix}/images/{uuid.uuid4().hex}{file_ext}"
                if self.prefix
                else f"images/{uuid.uuid4().hex}{file_ext}"
            )
            logger.info(f"Generated object key: {object_key}")
            self.client.put_object(
                Bucket=self.bucket_name, Body=content, Key=object_key
            )
            file_url = self._get_download_url(self.bucket_name, self.region, object_key)
            logger.info(f"Successfully uploaded bytes to COS: {file_url}")
            return file_url
        except Exception as e:
            logger.error(f"Failed to upload bytes to COS: {str(e)}")
            traceback.print_exc()
            return ""


class MinioStorage(Storage):
    """MinIO storage implementation"""

    def __init__(self, storage_config=None):
        """Initialize MinIO storage

        Args:
            storage_config: Storage configuration
        """
        self.storage_config = storage_config
        self.client, self.bucket_name, self.use_ssl, self.endpoint, self.path_prefix = (
            self._init_minio_client()
        )

    def _init_minio_client(self):
        """Initialize MinIO client from environment variables or injected config.

        If storage_config contains valid configuration, prefer those values
        to override environment variables.
        """
        try:
            # Get configuration from storage_config with environment variables as fallback
            # Each field can independently fall back to environment variables
            access_key = (
                self.storage_config.get("access_key_id")
                if self.storage_config and self.storage_config.get("access_key_id")
                else CONFIG.minio_access_key_id
            )
            secret_key = (
                self.storage_config.get("secret_access_key")
                if self.storage_config and self.storage_config.get("secret_access_key")
                else CONFIG.minio_secret_access_key
            )
            bucket_name = (
                self.storage_config.get("bucket_name")
                if self.storage_config and self.storage_config.get("bucket_name")
                else CONFIG.minio_bucket_name
            )
            path_prefix_raw = (
                self.storage_config.get("path_prefix")
                if self.storage_config and self.storage_config.get("path_prefix")
                else CONFIG.minio_path_prefix
            )
            path_prefix = path_prefix_raw.strip().strip("/") if path_prefix_raw else ""

            endpoint = CONFIG.minio_endpoint
            use_ssl = CONFIG.minio_use_ssl

            if not all([endpoint, access_key, secret_key, bucket_name]):
                logger.error(
                    "Incomplete MinIO configuration, missing environment variables"
                )
                return None, None, None, None, None

            # Initialize client
            client = Minio(
                endpoint, access_key=access_key, secret_key=secret_key, secure=use_ssl
            )

            # Ensure bucket exists
            found = client.bucket_exists(bucket_name)
            if not found:
                client.make_bucket(bucket_name)
                # Set public read policy for the bucket
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
            logger.error(f"Failed to initialize MinIO client: {str(e)}")
            return None, None, None, None, None

    def _get_download_url(self, object_key: str):
        """Construct a public URL for MinIO object.

        If MINIO_PUBLIC_ENDPOINT is provided, use it; otherwise fallback to endpoint.
        """
        # 1. Use public endpoint if provided
        endpoint = CONFIG.minio_public_endpoint
        if endpoint:
            return f"{endpoint}/{self.bucket_name}/{object_key}"

        # 2. Use SSL if enabled
        if self.use_ssl:
            return f"https://{self.endpoint}/{self.bucket_name}/{object_key}"

        # 3. Use HTTP default
        return f"http://{self.endpoint}/{self.bucket_name}/{object_key}"

    def upload_file(self, file_path: str) -> str:
        """Upload file to MinIO

        Args:
            file_path: File path

        Returns:
            File URL
        """
        logger.info(f"Uploading file to MinIO: {file_path}")
        try:
            if not self.client:
                return ""

            # Generate object key, use UUID to avoid conflicts
            file_name = os.path.basename(file_path)
            object_key = (
                f"{self.path_prefix}/images/{uuid.uuid4().hex}{os.path.splitext(file_name)[1]}"
                if self.path_prefix
                else f"images/{uuid.uuid4().hex}{os.path.splitext(file_name)[1]}"
            )
            logger.info(f"Generated MinIO object key: {object_key}")

            # Upload file
            logger.info("Attempting to upload file to MinIO")
            with open(file_path, "rb") as file_data:
                file_size = os.path.getsize(file_path)
                self.client.put_object(
                    bucket_name=self.bucket_name or "",
                    object_name=object_key,
                    data=file_data,
                    length=file_size,
                    content_type="application/octet-stream",
                )

            # Get file URL
            file_url = self._get_download_url(object_key)

            logger.info(f"Successfully uploaded file to MinIO: {file_url}")
            return file_url

        except Exception as e:
            logger.error(f"Failed to upload file to MinIO: {str(e)}")
            return ""

    def upload_bytes(self, content: bytes, file_ext: str = ".png") -> str:
        """Upload bytes to MinIO

        Args:
            content: Byte content to upload
            file_ext: File extension

        Returns:
            File URL
        """
        try:
            logger.info(f"Uploading bytes content to MinIO, size: {len(content)} bytes")
            if not self.client:
                return ""

            object_key = (
                f"{self.path_prefix}/images/{uuid.uuid4().hex}{file_ext}"
                if self.path_prefix
                else f"images/{uuid.uuid4().hex}{file_ext}"
            )
            logger.info(f"Generated MinIO object key: {object_key}")
            self.client.put_object(
                self.bucket_name or "",
                object_key,
                data=io.BytesIO(content),
                length=len(content),
                content_type="application/octet-stream",
            )
            file_url = self._get_download_url(object_key)
            logger.info(f"Successfully uploaded bytes to MinIO: {file_url}")
            return file_url
        except Exception as e:
            logger.error(f"Failed to upload bytes to MinIO: {str(e)}")
            traceback.print_exc()
            return ""


class LocalStorage(Storage):
    """Local file system storage implementation"""

    def __init__(self, storage_config: Dict[str, str] = {}):
        self.storage_config = storage_config
        base_dir = storage_config.get("base_dir", CONFIG.local_storage_base_dir)
        self.image_dir = os.path.join(base_dir, "images")
        os.makedirs(self.image_dir, exist_ok=True)

    def upload_file(self, file_path: str) -> str:
        logger.info(f"Uploading file to local storage: {file_path}")
        return file_path

    def upload_bytes(self, content: bytes, file_ext: str = ".png") -> str:
        logger.info(f"Uploading file to local storage: {len(content)} bytes")
        fname = os.path.join(self.image_dir, f"{uuid.uuid4()}{file_ext}")
        with open(fname, "wb") as f:
            f.write(content)
        return fname


class Base64Storage(Storage):
    def upload_file(self, file_path: str) -> str:
        logger.info(f"Uploading file to base64 storage: {file_path}")
        return file_path

    def upload_bytes(self, content: bytes, file_ext: str = ".png") -> str:
        logger.info(f"Uploading file to base64 storage: {len(content)} bytes")
        file_ext = file_ext.lstrip(".")
        return f"data:image/{file_ext};base64,{endecode.decode_image(content)}"


class DummyStorage(Storage):
    """Dummy storage implementation.

    It is used in tests or in environments where object storage is disabled.
    All upload methods return empty string.
    """

    def upload_file(self, file_path: str) -> str:
        return ""

    def upload_bytes(self, content: bytes, file_ext: str = ".png") -> str:
        return ""


def create_storage(storage_config: Dict[str, str] | None = None) -> Storage:
    """Create a storage instance based on configuration or environment variables

    Args:
        storage_config: Storage configuration dictionary

    Returns:
        Storage instance
    """
    storage_type = CONFIG.storage_type
    if storage_config:
        storage_type = str(storage_config.get("provider", storage_type)).lower()
    logger.info(f"Creating {storage_type} storage instance")

    if storage_type == "minio":
        return MinioStorage(storage_config)
    elif storage_type == "cos":
        return CosStorage(storage_config)
    elif storage_type == "local":
        return LocalStorage(storage_config or {})
    elif storage_type == "base64":
        return Base64Storage()
    return DummyStorage()
