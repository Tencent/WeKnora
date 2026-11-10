.PHONY: proto build run docker-build docker-run clean

# 生成 protobuf 代码
proto:
	@echo "Generating protobuf code..."
	@sh ./scripts/generate_proto.sh

# 构建 Go 客户端
build:
	@echo "Building Go client..."
	@go build -o bin/client ./src/client

# 运行 Python 服务
run:
	@echo "Running Python server..."
	@python src/server/server.py

# 清理
clean:
	@echo "Cleaning up..."
	@rm -rf bin/
	@find . -name "*.pyc" -delete
	@find . -name "__pycache__" -delete 