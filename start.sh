#!/bin/bash

set -euo pipefail

# ============== 配置 ==============
BINARY_NAME="./hl_monitor"
CONFIG_FILE="${CONFIG_FILE:-cfg.toml}"
PID_FILE="hl_monitor.pid"
LOG_FILE="logs/error.log"
WAIT_START=5                  # 启动检查等待时间(秒)

# ============== 颜色输出 ==============
readonly RED='\033[0;31m'
readonly GREEN='\033[0;32m'
readonly YELLOW='\033[1;33m'
readonly BLUE='\033[0;34m'
readonly NC='\033[0m'

# ============== 日志函数 ==============
log_info()    { echo -e "${BLUE}[INFO]${NC} $*"; }
log_success() { echo -e "${GREEN}[OK]${NC} $*"; }
log_warn()    { echo -e "${YELLOW}[WARN]${NC} $*"; }
log_error()   { echo -e "${RED}[ERROR]${NC} $*"; }

# ============== 工具函数 ==============

# 检查进程是否存在
process_exists() {
    local pid=$1
    kill -0 "$pid" 2>/dev/null
}

# 停止已存在的进程
stop_existing() {
    local stopped=false

    # 检查 PID 文件
    if [ -f "$PID_FILE" ]; then
        local old_pid=$(cat "$PID_FILE")
        if process_exists "$old_pid"; then
            log_warn "发现已存在的进程 (PID: $old_pid)，正在停止..."
            kill "$old_pid" 2>/dev/null || true
            sleep 1
            if process_exists "$old_pid"; then
                log_warn "进程未响应，强制停止..."
                kill -9 "$old_pid" 2>/dev/null || true
            fi
            stopped=true
        fi
        rm -f "$PID_FILE"
    fi

    # 检查进程名
    local existing_pid=$(pgrep -x "hl_monitor" 2>/dev/null || true)
    if [ -n "$existing_pid" ]; then
        log_warn "发现运行中的 hl_monitor (PID: $existing_pid)，正在停止..."
        kill "$existing_pid" 2>/dev/null || true
        sleep 1
        if process_exists "$existing_pid"; then
            kill -9 "$existing_pid" 2>/dev/null || true
        fi
        stopped=true
    fi

    if [ "$stopped" = true ]; then
        sleep 1
    fi
}

# 检查二进制文件
check_binary() {
    if [ ! -f "$BINARY_NAME" ]; then
        log_error "二进制文件不存在: $BINARY_NAME"
        log_info "请先运行: make build"
        exit 1
    fi

    if [ ! -x "$BINARY_NAME" ]; then
        log_error "二进制文件没有执行权限: $BINARY_NAME"
        chmod +x "$BINARY_NAME"
    fi
}

# 检查配置文件
check_config() {
    if [ ! -f "$CONFIG_FILE" ]; then
        log_error "配置文件不存在: $CONFIG_FILE"

        # 尝试备用配置
        if [ -f "cfg.local.toml" ]; then
            log_warn "使用 cfg.local.toml 代替"
            CONFIG_FILE="cfg.local.toml"
        elif [ -f "cfg.toml" ]; then
            log_warn "使用 cfg.toml 代替"
            CONFIG_FILE="cfg.toml"
        else
            exit 1
        fi
    fi
}

# 创建日志目录
ensure_log_dir() {
    if [ ! -d "logs" ]; then
        mkdir -p logs
        log_info "创建日志目录: logs/"
    fi
}

# 检查进程启动状态
check_started() {
    local pid=$1
    local elapsed=0

    log_info "等待服务启动..."

    while [ $elapsed -lt $WAIT_START ]; do
        if ! process_exists "$pid"; then
            log_error "服务启动失败，进程已退出"
            log_error "请检查日志: $LOG_FILE"
            exit 1
        fi

        # 检查日志中是否有错误
        if [ -f "$LOG_FILE" ]; then
            if grep -q "error\|Error\|ERROR" "$LOG_FILE" 2>/dev/null; then
                log_warn "日志中发现错误信息，请检查"
            fi
        fi

        elapsed=$((elapsed + 1))
        sleep 1
    done

    if process_exists "$pid"; then
        return 0
    fi

    return 1
}

# ============== 主流程 ==============

main() {
    echo -e "${GREEN}=================================${NC}"
    echo -e "${GREEN}  uTrading HL Monitor - Start${NC}"
    echo -e "${GREEN}=================================${NC}"
    echo ""

    # 1. 检查二进制
    check_binary

    # 2. 检查配置
    check_config

    # 3. 停止已存在的进程
    stop_existing

    # 4. 创建日志目录
    ensure_log_dir

    # 5. 启动服务
    log_info "启动服务..."
    log_info "配置: $CONFIG_FILE"

    nohup "$BINARY_NAME" -config "$CONFIG_FILE" >> "$LOG_FILE" 2>&1 &
    local pid=$!
    echo $pid > "$PID_FILE"

    # 6. 等待启动
    sleep 1

    if process_exists "$pid"; then
        # 7. 启动检查
        if check_started "$pid"; then
            echo ""
            echo -e "${GREEN}=================================${NC}"
            echo -e "${GREEN}  启动成功!${NC}"
            echo -e "${GREEN}=================================${NC}"
            echo ""
            echo -e "  PID:   ${GREEN}$pid${NC}"
            echo -e "  配置:  ${GREEN}$CONFIG_FILE${NC}"
            echo -e "  日志:  ${GREEN}$LOG_FILE${NC}"
            echo ""
            echo -e "  停止服务: ${YELLOW}./stop.sh${NC}"
            echo -e "  查看日志: ${YELLOW}tail -f $LOG_FILE${NC}"
            echo ""
            exit 0
        fi
    else
        log_error "服务启动失败"
        rm -f "$PID_FILE"
        exit 1
    fi
}

# 执行主流程
main "$@"
