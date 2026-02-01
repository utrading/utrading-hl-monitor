# 多阶段构建
FROM golang:1.25 AS builder

WORKDIR /app

# 安装必要工具
RUN apk add --no-cache git ca-certificates tzdata

# 复制依赖文件
COPY go.mod go.sum ./
RUN go mod download

# 复制源代码
COPY . .

# 编译
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o hl_monitor ./cmd/hl_monitor

# 运行阶段
FROM alpine:3.19

RUN apk add --no-cache ca-certificates tzdata && \
    mkdir -p /app/logs

WORKDIR /app

# 从构建阶段复制二进制文件
COPY --from=builder /app/hl_monitor .
COPY --from=builder /app/cfg.toml .

# 暴露端口
EXPOSE 8080

# 设置时区
ENV TZ=Asia/Shanghai

# 运行
ENTRYPOINT ["./hl_monitor"]
CMD ["-config", "cfg.toml"]
