#!/usr/bin/env python
# -*- coding: utf-8 -*-

import sys
import os
import logging
from paddleocr import PaddleOCR

# 添加当前目录到Python路径
current_dir = os.path.dirname(os.path.abspath(__file__))
if current_dir not in sys.path:
    sys.path.append(current_dir)

# 导入ImageParser
from parser.image_parser import ImageParser

# 配置日志
logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s - %(name)s - %(levelname)s - %(message)s",
    datefmt="%Y-%m-%d %H:%M:%S",
)
logger = logging.getLogger(__name__)


def init_ocr_model():
    """Initialize PaddleOCR model to pre-download and cache models"""
    try:
        logger.info("Initializing PaddleOCR model for pre-download...")
        
        # 使用与代码中相同的配置
        ocr_config = {
            "use_gpu": False,
            "text_det_limit_type": "max",
            "text_det_limit_side_len": 960,
            "use_doc_orientation_classify": True,  # 启用文档方向分类
            "use_doc_unwarping": False,
            "use_textline_orientation": True,  # 启用文本行方向检测
            "text_recognition_model_name": "PP-OCRv4_server_rec",
            "text_detection_model_name": "PP-OCRv4_server_det",
            "text_det_thresh": 0.3,
            "text_det_box_thresh": 0.6,
            "text_det_unclip_ratio": 1.5,
            "text_rec_score_thresh": 0.0,
            "ocr_version": "PP-OCRv4",
            "lang": "ch",
            "show_log": False,
            "use_dilation": True,
            "det_db_score_mode": "slow",
        }
        
        # 初始化PaddleOCR，这会触发模型下载和缓存
        ocr = PaddleOCR(**ocr_config)
        logger.info("PaddleOCR model initialization completed successfully")
        
        # 测试OCR功能以确保模型正常工作
        import numpy as np
        from PIL import Image
        
        # 创建一个简单的测试图像
        test_image = np.ones((100, 300, 3), dtype=np.uint8) * 255
        test_pil = Image.fromarray(test_image)
        
        # 执行一次OCR测试
        result = ocr.ocr(np.array(test_pil), cls=False)
        logger.info("PaddleOCR test completed successfully")
        
    except Exception as e:
        logger.error(f"Failed to initialize PaddleOCR model: {str(e)}")
        raise
