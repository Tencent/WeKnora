# Build stage
FROM golang:1.24-alpine AS builder

WORKDIR /app

# 通过构建参数接收敏感信息
ARG GOPRIVATE_ARG
ARG GOPROXY_ARG
ARG GOSUMDB_ARG=off
ARG APK_MIRROR_ARG="mirrors.tencent.com"

# 设置Go环境变量
ENV GOPRIVATE=${GOPRIVATE_ARG}
ENV GOPROXY=${GOPROXY_ARG}
ENV GOSUMDB=${GOSUMDB_ARG}

# Install dependencies
RUN if [ -n "$APK_MIRROR_ARG" ]; then \
        sed -i "s@dl-cdn.alpinelinux.org@${APK_MIRROR_ARG}@g" /etc/apk/repositories; \
    fi && \
    apk add --no-cache git build-base

# Install migrate tool
RUN go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest

# Copy go mod and sum files
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .

# Get version and commit info for build injection
ARG VERSION_ARG
ARG COMMIT_ID_ARG
ARG BUILD_TIME_ARG
ARG GO_VERSION_ARG

# Set build-time variables
ENV VERSION=${VERSION_ARG}
ENV COMMIT_ID=${COMMIT_ID_ARG}
ENV BUILD_TIME=${BUILD_TIME_ARG}
ENV GO_VERSION=${GO_VERSION_ARG}

# Build the application with version info
RUN --mount=type=cache,target=/go/pkg/mod make build-prod
RUN --mount=type=cache,target=/go/pkg/mod cp -r /go/pkg/mod/github.com/yanyiwu/ /app/yanyiwu/

# Final stage
FROM alpine:3.23

WORKDIR /app

ARG APK_MIRROR_ARG="mirrors.tencent.com"

RUN if [ -n "$APK_MIRROR_ARG" ]; then \
        sed -i "s@dl-cdn.alpinelinux.org@${APK_MIRROR_ARG}@g" /etc/apk/repositories; \
    fi && \
    apk update && apk upgrade && \
    apk add --no-cache build-base postgresql-client mysql-client ca-certificates tzdata sed curl bash vim wget \
        nodejs npm python3 py3-pip python3-dev libffi-dev openssl-dev cargo && \
    python3 -m pip install --upgrade pip setuptools wheel && \
    # 使用官方安装脚本安装 uvx
    curl -LsSf https://astral.sh/uv/install.sh | sh && \
    ln -sf /root/.cargo/bin/uvx /usr/local/bin/uvx

# Create a non-root user and switch to it
RUN mkdir -p /data/files && \
    id -u appuser >/dev/null 2>&1 || adduser -D -g '' appuser && \
    chown -R appuser:appuser /app /data/files

# Copy migrate tool from builder stage
COPY --from=builder /go/bin/migrate /usr/local/bin/
COPY --from=builder /app/yanyiwu/ /go/pkg/mod/github.com/yanyiwu/

# Copy the binary from the builder stage
COPY --from=builder /app/config ./config
COPY --from=builder /app/scripts ./scripts
COPY --from=builder /app/migrations ./migrations
COPY --from=builder /app/dataset/samples ./dataset/samples
COPY --from=builder /app/WeKnora .

# Make scripts executable
RUN chmod +x ./scripts/*.sh

# Expose ports
EXPOSE 8080

# Switch to non-root user and run the application directly
USER appuser

CMD ["./WeKnora"]
