# -*- coding: utf-8 -*-
import os
import uuid
import logging
import io
import traceback
from abc import ABC, abstractmethod
from typing import Tuple, Optional

from qcloud_cos import CosConfig, CosS3Client
from minio import Minio

logger = logging.getLogger(__name__)
logger.setLevel(logging.INFO)


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
        self.client, self.bucket_name, self.region, self.prefix = self._init_cos_client()
        
    def _init_cos_client(self):
        """Initialize Tencent Cloud COS client"""
        try:
            # Use provided COS config if available, otherwise fall back to environment variables
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
                secret_id = os.getenv("COS_SECRET_ID")
                secret_key = os.getenv("COS_SECRET_KEY")
                region = os.getenv("COS_REGION")
                bucket_name = os.getenv("COS_BUCKET_NAME")
                appid = os.getenv("COS_APP_ID")
                prefix = os.getenv("COS_PATH_PREFIX")
                
            enable_old_domain = (
                os.getenv("COS_ENABLE_OLD_DOMAIN", "true").lower() == "true"
            )

            if not all([secret_id, secret_key, region, bucket_name, appid]):
                logger.error(
                    "Incomplete COS configuration, missing required environment variables"
                    f"secret_id: {secret_id}, secret_key: {secret_key}, region: {region}, bucket_name: {bucket_name}, appid: {appid}"
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
            file_name = os.path.basename(file_path)
            object_key = (
                f"{self.prefix}/images/{uuid.uuid4().hex}{os.path.splitext(file_name)[1]}"
            )
            logger.info(f"Generated object key: {object_key}")

            # Upload file
            logger.info("Attempting to upload file to COS")
            response = self.client.upload_file(
                Bucket=self.bucket_name, LocalFilePath=file_path, Key=object_key
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
                
            object_key = f"{self.prefix}/images/{uuid.uuid4().hex}{file_ext}" if self.prefix else f"images/{uuid.uuid4().hex}{file_ext}"
            logger.info(f"Generated object key: {object_key}")
            self.client.put_object(Bucket=self.bucket_name, Body=content, Key=object_key)
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
        self.client, self.bucket_name, self.use_ssl, self.endpoint, self.path_prefix = self._init_minio_client()
        
    def _init_minio_client(self):
        """Initialize MinIO client from environment variables or injected config.

        If storage_config.path_prefix contains JSON from server (for minio case),
        prefer those values to override envs.
        """
        try:
            endpoint = os.getenv("MINIO_ENDPOINT")
            use_ssl = os.getenv("MINIO_USE_SSL", "false").lower() == "true"
            if self.storage_config and self.storage_config.get("bucket_name"):
                storage_config = self.storage_config
                bucket_name = storage_config.get("bucket_name")
                path_prefix = storage_config.get("path_prefix").strip().strip("/")
                access_key = storage_config.get("access_key_id")
                secret_key = storage_config.get("secret_access_key")
            else:
                access_key = os.getenv("MINIO_ACCESS_KEY_ID")
                secret_key = os.getenv("MINIO_SECRET_ACCESS_KEY")
                bucket_name = os.getenv("MINIO_BUCKET_NAME")
                path_prefix = os.getenv("MINIO_PATH_PREFIX", "").strip().strip("/")

            if not all([endpoint, access_key, secret_key, bucket_name]):
                logger.error("Incomplete MinIO configuration, missing required environment variables")
                return None, None, None, None, None

            # Initialize client
            client = Minio(endpoint, access_key=access_key, secret_key=secret_key, secure=use_ssl)

            # Ensure bucket exists
            found = client.bucket_exists(bucket_name)
            if not found:
                client.make_bucket(bucket_name)
                policy = '{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"AWS":["*"]},"Action":["s3:GetBucketLocation","s3:ListBucket"],"Resource":["arn:aws:s3:::%s"]},{"Effect":"Allow","Principal":{"AWS":["*"]},"Action":["s3:GetObject"],"Resource":["arn:aws:s3:::%s/*"]}]}' % (bucket_name, bucket_name)
                client.set_bucket_policy(bucket_name, policy)

            return client, bucket_name, use_ssl, endpoint, path_prefix
        except Exception as e:
            logger.error(f"Failed to initialize MinIO client: {str(e)}")
            return None, None, None, None, None
            
    def _get_download_url(self, bucket_name: str, object_key: str, use_ssl: bool, endpoint: str, public_endpoint: str = None):
        """Construct a public URL for MinIO object.

        If MINIO_PUBLIC_ENDPOINT is provided, use it; otherwise fallback to endpoint.
        """
        if public_endpoint:
            base = public_endpoint
        else:
            scheme = "https" if use_ssl else "http"
            base = f"{scheme}://{endpoint}"
        # Path-style URL for MinIO
        return f"{base}/{bucket_name}/{object_key}"
        
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
            object_key = f"{self.path_prefix}/images/{uuid.uuid4().hex}{os.path.splitext(file_name)[1]}" if self.path_prefix else f"images/{uuid.uuid4().hex}{os.path.splitext(file_name)[1]}"
            logger.info(f"Generated MinIO object key: {object_key}")

            # Upload file
            logger.info("Attempting to upload file to MinIO")
            with open(file_path, 'rb') as file_data:
                file_size = os.path.getsize(file_path)
                self.client.put_object(
                    bucket_name=self.bucket_name,
                    object_name=object_key,
                    data=file_data,
                    length=file_size,
                    content_type='application/octet-stream'
                )

            # Get file URL
            file_url = self._get_download_url(
                self.bucket_name, 
                object_key, 
                self.use_ssl, 
                self.endpoint,
                os.getenv("MINIO_PUBLIC_ENDPOINT", None)
            )

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
                
            object_key = f"{self.path_prefix}/images/{uuid.uuid4().hex}{file_ext}" if self.path_prefix else f"images/{uuid.uuid4().hex}{file_ext}"
            logger.info(f"Generated MinIO object key: {object_key}")
            self.client.put_object(
                self.bucket_name, 
                object_key, 
                data=io.BytesIO(content), 
                length=len(content), 
                content_type="application/octet-stream"
            )
            file_url = self._get_download_url(
                self.bucket_name, 
                object_key, 
                self.use_ssl, 
                self.endpoint,
                os.getenv("MINIO_PUBLIC_ENDPOINT", None)
            )
            logger.info(f"Successfully uploaded bytes to MinIO: {file_url}")
            return file_url
        except Exception as e:
            logger.error(f"Failed to upload bytes to MinIO: {str(e)}")
            traceback.print_exc()
            return ""


def create_storage(storage_config=None) -> Storage:
    """Create a storage instance based on configuration or environment variables
    
    Args:
        storage_config: Storage configuration dictionary
        
    Returns:
        Storage instance
    """
    storage_type = os.getenv("STORAGE_TYPE", "cos").lower()
    
    if storage_config:
        storage_type = str(storage_config.get("provider", storage_type)).lower()
        
    logger.info(f"Creating {storage_type} storage instance")
    
    if storage_type == "minio":
        return MinioStorage(storage_config)
    elif storage_type == "cos":
        # Default to COS
        return CosStorage(storage_config)
    else:
        return None