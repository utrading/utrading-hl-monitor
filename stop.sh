#!/bin/bash

set -euo pipefail

# ============== 配置 ==============
BINARY_NAME="hl_monitor"
PID_FILE="hl_monitor.pid"
WAIT_TIMEOUT=15              # 优雅等待超时(秒)
WAIT_INTERVAL=1              # 检查间隔(秒)
GRACEFUL_STOP_WAIT=2         # SIGTERM 等待时间(秒)

# ============== 颜色输出 ==============
readonly RED='\033[0;31m'
readonly GREEN='\033[0;32m'
readonly YELLOW='\033[1;33m'
readonly BLUE='\033[0;34m'
readonly GRAY='\033[0;90m'
readonly NC='\033[0m'

# ============== 日志函数 ==============
log_info()    { echo -e "${BLUE}[INFO]${NC} $*"; }
log_success() { echo -e "${GREEN}[OK]${NC} $*"; }
log_warn()    { echo -e "${YELLOW}[WARN]${NC} $*"; }
log_error()   { echo -e "${RED}[ERROR]${NC} $*"; }

# ============== 工具函数 ==============

# 获取当前用户
get_current_user() {
    whoami
}

# 检查是否有 sudo 权限
has_sudo() {
    command -v sudo >/dev/null 2>&1
}

# 检查进程是否存在
process_exists() {
    local pid=$1
    kill -0 "$pid" 2>/dev/null
}

# 获取进程所有者
get_process_owner() {
    local pid=$1
    ps -o user= -p "$pid" 2>/dev/null | tr -d ' ' || echo ""
}

# 发送信号（处理权限）
send_signal() {
    local pid=$1
    local signal=${2:-TERM}
    local owner=$(get_process_owner "$pid")
    local current_user=$(get_current_user)

    if [ "$owner" != "$current_user" ]; then
        if has_sudo; then
            sudo kill "-$signal" "$pid" 2>/dev/null
        else
            log_error "Cannot kill process $pid (owner: $owner). No sudo access."
            return 1
        fi
    else
        kill "-$signal" "$pid" 2>/dev/null
    fi
}

# 等待进程退出（带进度显示）
wait_for_process() {
    local pid=$1
    local timeout=${2:-$WAIT_TIMEOUT}
    local elapsed=0

    while process_exists "$pid"; do
        if [ $elapsed -ge $timeout ]; then
            return 1
        fi
        printf "\r${GRAY}等待中... %d/%d秒${NC}" "$elapsed" "$timeout"
        sleep $WAIT_INTERVAL
        elapsed=$((elapsed + WAIT_INTERVAL))
    done
    printf "\r%40s\r" " "  # 清除进度行
    return 0
}

# ============== 进程查找 ==============

# 从 PID 文件读取
get_pid_from_file() {
    if [ -f "$PID_FILE" ]; then
        cat "$PID_FILE" 2>/dev/null || echo ""
    else
        echo ""
    fi
}

# 通过进程名查找
find_pids_by_name() {
    pgrep -x "$BINARY_NAME" 2>/dev/null || true
}

# 获取所有需要停止的 PID
get_all_pids() {
    local pids=()
    local file_pid

    # 优先从 PID 文件
    file_pid=$(get_pid_from_file)
    if [ -n "$file_pid" ]; then
        if process_exists "$file_pid"; then
            pids+=("$file_pid")
        fi
    fi

    # 兜底：通过进程名查找（排除已获取的）
    local name_pids
    name_pids=$(find_pids_by_name)
    for pid in $name_pids; do
        if [ -n "$pid" ]; then
            local found=false
            for existing in "${pids[@]:-}"; do
                if [ "$existing" = "$pid" ]; then
                    found=true
                    break
                fi
            done
            if [ "$found" = false ]; then
                pids+=("$pid")
            fi
        fi
    done

    # 输出结果
    printf '%s\n' "${pids[@]:-}"
}

# ============== 停止逻辑 ==============

# 停止单个进程
stop_process() {
    local pid=$1
    local owner=$(get_process_owner "$pid")

    log_info "停止进程 $pid (所有者: $owner)"

    # 发送 SIGTERM
    if send_signal "$pid" TERM; then
        log_info "已发送 SIGTERM 信号"

        # 等待优雅退出
        if process_exists "$pid"; then
            sleep $GRACEFUL_STOP_WAIT

            # 继续等待（带进度）
            if process_exists "$pid"; then
                log_info "等待进程退出..."
                if wait_for_process "$pid"; then
                    log_success "进程 $pid 已停止"
                    return 0
                fi
            else
                log_success "进程 $pid 已停止"
                return 0
            fi
        fi
    fi

    # 超时强制结束
    if process_exists "$pid"; then
        log_warn "进程 $pid 未响应，强制结束..."
        if send_signal "$pid" KILL; then
            sleep 1
            if process_exists "$pid"; then
                log_error "无法停止进程 $pid"
                return 1
            else
                log_success "进程 $pid 已强制停止"
                return 0
            fi
        fi
    fi

    return 0
}

# ============== 主流程 ==============

main() {
    echo -e "${GREEN}=================================${NC}"
    echo -e "${GREEN}  uTrading HL Monitor - Stop${NC}"
    echo -e "${GREEN}=================================${NC}"
    echo ""

    # 获取所有需要停止的进程
    local pids=($(get_all_pids))

    if [ ${#pids[@]} -eq 0 ]; then
        log_warn "${BINARY_NAME} 没有运行"
        rm -f "$PID_FILE"
        exit 0
    fi

    log_info "找到 ${#pids[@]} 个运行中的进程"

    # 停止所有进程
    local failed=0
    for pid in "${pids[@]}"; do
        if ! stop_process "$pid"; then
            failed=1
        fi
    done

    # 清理 PID 文件
    rm -f "$PID_FILE"

    echo ""
    if [ $failed -eq 0 ]; then
        log_success "所有进程已停止"
        exit 0
    else
        log_error "部分进程停止失败"
        exit 1
    fi
}

# 执行主流程
main "$@"
