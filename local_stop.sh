#!/bin/bash

set -euo pipefail

# 获取脚本所在目录
BASEDIR=$(dirname "$(readlink -f "$0")")
cd "$BASEDIR"

BINARY="hl_monitor"

# 颜色定义
readonly GREEN='\033[0;32m'
readonly YELLOW='\033[1;33m'
readonly NC='\033[0m'

# 查找所有匹配的进程 PID
PIDS=$(pgrep -f "$BINARY" 2>/dev/null || true)

if [ -z "$PIDS" ]; then
    echo -e "${YELLOW}⚠ ${BINARY} 未运行${NC}"
    exit 0
fi

echo -e "${GREEN}=================================${NC}"
echo -e "${GREEN}  停止 uTrading HL Monitor${NC}"
echo -e "${GREEN}=================================${NC}"
echo ""

# 停止每个进程
for pid in $PIDS; do
    owner=$(ps -o user= -p "$pid" 2>/dev/null | tr -d ' ')
    current_user=$(whoami)

    echo -e "${GREEN}停止进程${NC} PID: $pid (所有者: $owner)"

    if [ "$owner" != "$current_user" ]; then
        if command -v sudo >/dev/null 2>&1; then
            sudo kill -TERM "$pid"
        else
            echo -e "${YELLOW}  警告: 需要 sudo 权限停止 $owner 的进程${NC}"
            kill -TERM "$pid" 2>/dev/null || true
        fi
    else
        kill -TERM "$pid"
    fi

    # 等待进程退出（最多 5 秒）
    for i in {1..5}; do
        if ! kill -0 "$pid" 2>/dev/null; then
            echo -e "${GREEN}  ✓ 进程 $pid 已停止${NC}"
            break
        fi
        if [ $i -eq 5 ]; then
            echo -e "${YELLOW}  ! 进程 $pid 未响应，强制结束...${NC}"
            if [ "$owner" != "$current_user" ] && command -v sudo >/dev/null 2>&1; then
                sudo kill -KILL "$pid" 2>/dev/null || true
            else
                kill -KILL "$pid" 2>/dev/null || true
            fi
        fi
        sleep 1
    done
done

# 清理 PID 文件
rm -f "${BASEDIR}/hl_monitor.pid"

echo ""
echo -e "${GREEN}✓ 停止完成${NC}"
