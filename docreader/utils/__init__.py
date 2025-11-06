#
#  Copyright 2024 The InfiniFlow Authors. All Rights Reserved.
#
#  Licensed under the Apache License, Version 2.0 (the "License");
#  you may not use this file except in compliance with the License.
#  You may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
#  Unless required by applicable law or agreed to in writing, software
#  distributed under the License is distributed on an "AS IS" BASIS,
#  WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
#  See the License for the specific language governing permissions and
#  limitations under the License.
#

import os
import re
import logging

# 配置日志
logger = logging.getLogger(__name__)


def singleton(cls, *args, **kw):
    instances = {}

    def _singleton():
        key = str(cls) + str(os.getpid())
        if key not in instances:
            logger.info(f"Creating new singleton instance with key: {key}")
            instances[key] = cls(*args, **kw)
        else:
            logger.info(f"Returning existing singleton instance with key: {key}")
        return instances[key]

    return _singleton


def rmSpace(txt):
    logger.info(f"Removing spaces from text of length: {len(txt)}")
    txt = re.sub(r"([^a-z0-9.,\)>]) +([^ ])", r"\1\2", txt, flags=re.IGNORECASE)
    return re.sub(r"([^ ]) +([^a-z0-9.,\(<])", r"\1\2", txt, flags=re.IGNORECASE)


def findMaxDt(fnm):
    m = "1970-01-01 00:00:00"
    logger.info(f"Finding maximum date in file: {fnm}")
    try:
        with open(fnm, "r") as f:
            while True:
                l = f.readline()
                if not l:
                    break
                l = l.strip("\n")
                if l == "nan":
                    continue
                if l > m:
                    m = l
        logger.info(f"Maximum date found: {m}")
    except Exception as e:
        logger.error(f"Error reading file {fnm} for max date: {str(e)}")
    return m


def findMaxTm(fnm):
    m = 0
    logger.info(f"Finding maximum time in file: {fnm}")
    try:
        with open(fnm, "r") as f:
            while True:
                l = f.readline()
                if not l:
                    break
                l = l.strip("\n")
                if l == "nan":
                    continue
                if int(l) > m:
                    m = int(l)
        logger.info(f"Maximum time found: {m}")
    except Exception as e:
        logger.error(f"Error reading file {fnm} for max time: {str(e)}")
    return m
