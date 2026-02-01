#!/bin/bash

set -euo pipefail

# ============== 配置 ==============
BASEDIR=$(dirname "$(readlink -f "$0")")
cd "$BASEDIR"

BINARY_NAME="./hl_monitor"
CONFIG_FILE="${CONFIG_FILE:-cfg.local.toml}"
PID_FILE="hl_monitor.pid"
LOG_FILE="logs/error.log"

# ============== 颜色输出 ==============
readonly GREEN='\033[0;32m'
readonly YELLOW='\033[1;33m'
readonly RED='\033[0;31m'
readonly BLUE='\033[0;34m'
readonly NC='\033[0m'

# ============== 工具函数 ==============

# 进程检查
process_exists() {
    kill -0 "$1" 2>/dev/null
}

# 停止已存在的进程
stop_existing() {
    local stopped=false

    # 检查 PID 文件
    if [ -f "$PID_FILE" ]; then
        local pid=$(cat "$PID_FILE")
        if process_exists "$pid"; then
            echo -e "${YELLOW}▸ 停止已存在的进程 (PID: $pid)${NC}"
            kill "$pid" 2>/dev/null || true
            sleep 1
            stopped=true
        fi
        rm -f "$PID_FILE"
    fi

    # 检查进程名
    local pids=$(pgrep -f "$BASEDIR/hl_monitor" 2>/dev/null || true)
    if [ -n "$pids" ]; then
        for pid in $pids; do
            echo -e "${YELLOW}▸ 停止进程 $pid${NC}"
            kill "$pid" 2>/dev/null || true
        done
        sleep 1
        stopped=true
    fi

    if [ "$stopped" = true ]; then
        # 等待完全停止
        for i in {1..3}; do
            if ! pgrep -f "$BASEDIR/hl_monitor" >/dev/null 2>&1; then
                break
            fi
            sleep 1
        done
    fi
}

# 检查配置文件
check_config() {
    if [ ! -f "$CONFIG_FILE" ]; then
        echo -e "${YELLOW}▸ 配置文件不存在: $CONFIG_FILE${NC}"

        # 尝试备用配置
        if [ -f "cfg.toml" ]; then
            echo -e "${YELLOW}▸ 使用 cfg.toml 代替${NC}"
            CONFIG_FILE="cfg.toml"
        else
            echo -e "${RED}✗ 错误: 找不到配置文件${NC}"
            exit 1
        fi
    fi
}

# 编译
build_binary() {
    echo -e "${BLUE}[2/4]${NC} 编译..."
    if go build -o "$BINARY_NAME" ./cmd/hl_monitor; then
        echo -e "${GREEN}  ✓ 编译成功${NC}"
    else
        echo -e "${RED}  ✗ 编译失败${NC}"
        exit 1
    fi

    # 确保可执行
    chmod +x "$BINARY_NAME" 2>/dev/null || true
}

# 创建日志目录
ensure_log_dir() {
    if [ ! -d "logs" ]; then
        mkdir -p logs
    fi
}

# ============== 主流程 ==============

main() {
    echo -e "${GREEN}=================================${NC}"
    echo -e "${GREEN}  uTrading HL Monitor - 启动${NC}"
    echo -e "${GREEN}=================================${NC}"
    echo ""

    # 1. 检查配置
    check_config

    # 2. 停止已存在的进程
    echo -e "${BLUE}[1/4]${NC} 检查并停止已存在的进程..."
    stop_existing

    # 3. 编译
    build_binary

    # 4. 启动服务
    echo -e "${BLUE}[3/4]${NC} 配置: $CONFIG_FILE"
    echo -e "${BLUE}[4/4]${NC} 启动服务..."

    ensure_log_dir

    # 后台启动
    nohup "$BINARY_NAME" -config "$CONFIG_FILE" > "$LOG_FILE" 2>&1 &
    local pid=$!
    echo $pid > "$PID_FILE"

    # 等待启动
    sleep 2

    # 检查启动状态
    if process_exists "$pid"; then
        echo ""
        echo -e "${GREEN}=================================${NC}"
        echo -e "${GREEN}  ✓ 启动成功!${NC}"
        echo -e "${GREEN}=================================${NC}"
        echo ""
        echo -e "  PID:   ${GREEN}$pid${NC}"
        echo -e "  配置:  ${GREEN}$CONFIG_FILE${NC}"
        echo -e "  日志:  ${GREEN}$LOG_FILE${NC}"
        echo ""
        echo -e "  ${BLUE}./local_stop.sh${NC}    - 停止服务"
        echo -e "  ${BLUE}tail -f $LOG_FILE${NC}  - 查看日志"
        echo ""

        # 可选：查看日志
        read -p "是否立即查看日志? [y/N] " -n 1 -r
        echo
        if [[ $REPLY =~ ^[Yy]$ ]]; then
            tail -f "$LOG_FILE"
        fi
    else
        echo ""
        echo -e "${RED}✗ 启动失败，请检查日志: $LOG_FILE${NC}"
        rm -f "$PID_FILE"
        exit 1
    fi
}

# 执行主流程
main "$@"
