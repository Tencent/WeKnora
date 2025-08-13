# Build stage
FROM golang:1.24-alpine AS builder

WORKDIR /app

# Install dependencies
RUN sed -i 's/dl-cdn.alpinelinux.org/mirrors.tuna.tsinghua.edu.cn/g' /etc/apk/repositories && \
    apk add --no-cache git build-base

# 通过构建参数接收敏感信息
ARG GOPRIVATE_ARG
ARG GOPROXY_ARG
ARG GOSUMDB_ARG=off

# 设置Go环境变量
ENV GOPRIVATE=${GOPRIVATE_ARG}
ENV GOPROXY=${GOPROXY_ARG}
ENV GOSUMDB=${GOSUMDB_ARG}

# Copy go mod and sum files
COPY go.mod go.sum ./
RUN go mod download

ENV CGO_ENABLED=1
# Install migrate tool
RUN go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest

# Copy source code
COPY . .

# Build the application
RUN make build-prod

# Final stage
FROM alpine:3.17

WORKDIR /app

# Install runtime dependencies
RUN sed -i 's/dl-cdn.alpinelinux.org/mirrors.tuna.tsinghua.edu.cn/g' /etc/apk/repositories && \
    apk update && apk upgrade && \
    apk add --no-cache build-base postgresql-client mysql-client ca-certificates tzdata sed curl bash supervisor vim wget

# Copy the binary from the builder stage
COPY --from=builder /app/WeKnora .
COPY --from=builder /app/config ./config
COPY --from=builder /app/scripts ./scripts
COPY --from=builder /app/migrations ./migrations
COPY --from=builder /app/dataset/samples ./dataset/samples

# Copy migrate tool from builder stage
COPY --from=builder /go/bin/migrate /usr/local/bin/
COPY --from=builder /go/pkg/mod/github.com/yanyiwu /go/pkg/mod/github.com/yanyiwu/

# Make scripts executable
RUN chmod +x ./scripts/*.sh

# Setup supervisor configuration
RUN mkdir -p /etc/supervisor.d/
COPY docker/config/supervisord.conf /etc/supervisor.d/supervisord.conf

# Expose ports
EXPOSE 8080

# Set environment variables
ENV CGO_ENABLED=1

# Create a non-root user and switch to it
RUN mkdir -p /data/files && \
    adduser -D -g '' appuser && \
    chown -R appuser:appuser /app /data/files

# Run supervisor instead of direct application start
CMD ["supervisord", "-c", "/etc/supervisor.d/supervisord.conf"]