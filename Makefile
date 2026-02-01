.PHONY: build run stop restart clean test help docker-up docker-down docker-logs docker-ps

# 编译二进制文件
build:
	@echo "Building hl_monitor..."
	@go build -o hl_monitor ./cmd/hl_monitor
	@echo "Build complete: ./hl_monitor"

# 启动服务（后台运行）
start:
	@echo "Starting hl_monitor..."
	@./start.sh

# 停止服务
stop:
	@echo "Stopping hl_monitor..."
	@./stop.sh

# 重启服务
restart: stop start

# 运行服务（前台）
run: build
	@echo "Starting hl_monitor (foreground)..."
	@./hl_monitor -config cfg.local.toml

# 查看日志
logs:
	@tail -f logs/output.log

# 清理构建产物
clean:
	@echo "Cleaning..."
	@rm -f hl_monitor hl_monitor.pid
	@echo "Clean complete"

# 运行测试
test:
	@echo "Running tests..."
	@go test ./... -v

# 下载依赖
deps:
	@echo "Downloading dependencies..."
	@go mod download
	@go mod tidy

# Docker - 启动依赖服务
docker-up:
	@echo "Starting Docker services (MySQL, NATS)..."
	@docker-compose up -d

# Docker - 停止服务
docker-down:
	@echo "Stopping Docker services..."
	@docker-compose down

# Docker - 查看日志
docker-logs:
	@docker-compose logs -f

# Docker - 查看服务状态
docker-ps:
	@docker-compose ps

# Docker - 重启服务
docker-restart: docker-down docker-up

# 帮助信息
help:
	@echo "Available targets:"
	@echo ""
	@echo "Build & Run:"
	@echo "  build   - Build hl_monitor binary"
	@echo "  start   - Build and start in background"
	@echo "  stop    - Stop running service"
	@echo "  restart - Restart service"
	@echo "  run     - Build and run in foreground"
	@echo "  logs    - Tail log file"
	@echo ""
	@echo "Docker:"
	@echo "  docker-up      - Start MySQL and NATS"
	@echo "  docker-down    - Stop Docker services"
	@echo "  docker-logs    - View Docker logs"
	@echo "  docker-ps      - Show Docker services status"
	@echo "  docker-restart - Restart Docker services"
	@echo ""
	@echo "Other:"
	@echo "  clean   - Remove build artifacts"
	@echo "  test    - Run tests"
	@echo "  deps    - Download and tidy dependencies"
