# Build stage
FROM golang:1.25-alpine AS builder

WORKDIR /build

# 复制依赖文件
COPY go.mod go.sum ./
RUN go mod download

# 复制源码
COPY . .

# 构建二进制（CGO_ENABLED=0 静态编译，适配 alpine）
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /rent-scout ./cmd/rent-scout

# Runtime stage
FROM alpine:3.21

# 安装 ca-certificates（HTTPS 请求必需）和 tzdata（时区支持）
RUN apk add --no-cache ca-certificates tzdata && \
    adduser -D -u 1000 rentscout

# 创建工作目录与挂载点
WORKDIR /app
RUN mkdir -p /app/config /app/db && chown -R rentscout:rentscout /app

# 复制二进制
COPY --from=builder /rent-scout /usr/local/bin/rent-scout

# 切换非 root
USER rentscout

# 默认配置文件路径（可通过环境变量覆盖）
ENV CONFIG_PATH=/app/config/config.toml
ENV ENV_PATH=/app/config/config.env.local.toml
ENV DB_PATH=/app/db/rent-scout.db

# 暴露端口（与 config.toml server.addr 一致，默认 7777）
EXPOSE 7777

# 入口
ENTRYPOINT ["/usr/local/bin/rent-scout"]
