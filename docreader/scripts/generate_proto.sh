#!/bin/bash
set -x

# 设置目录
PROTO_DIR="docreader/proto"
PYTHON_OUT="docreader/proto"
GO_OUT="docreader/proto"

# 生成Python代码
python3 -m grpc_tools.protoc -I${PROTO_DIR} \
    --python_out=${PYTHON_OUT} \
    --pyi_out=${PYTHON_OUT} \
    --grpc_python_out=${PYTHON_OUT} \
    ${PROTO_DIR}/docreader.proto

# 生成Go代码
protoc -I${PROTO_DIR} --go_out=${GO_OUT} \
    --go_opt=paths=source_relative \
    --go-grpc_out=${GO_OUT} \
    --go-grpc_opt=paths=source_relative \
    ${PROTO_DIR}/docreader.proto

# 修复Python导入问题（MacOS兼容版本）
if [ "$(uname)" == "Darwin" ]; then
    # MacOS版本
    sed -i '' 's/import docreader_pb2/from docreader.proto import docreader_pb2/g' ${PYTHON_OUT}/docreader_pb2_grpc.py
else
    # Linux版本
    sed -i 's/import docreader_pb2/from docreader.proto import docreader_pb2/g' ${PYTHON_OUT}/docreader_pb2_grpc.py
fi

echo "Proto files generated successfully!"