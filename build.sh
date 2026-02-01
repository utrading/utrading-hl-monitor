#!/bin/bash
set -e

# 颜色输出
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

# Go 编译配置
export GOPROXY=https://goproxy.io,direct
export GOARCH=amd64
export GOOS=linux

# 构建信息
VERSION="${VERSION:-dev}"
BUILD_TIME=$(date -u '+%Y-%m-%d_%H:%M:%S')
GIT_COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")

LDFLAGS="-s -w -X main.Version=${VERSION} -X main.BuildTime=${BUILD_TIME} -X main.GitCommit=${GIT_COMMIT}"

echo -e "${GREEN}Building hl_monitor...${NC}"
echo -e "${YELLOW}Version: ${VERSION}${NC}"
echo -e "${YELLOW}Commit: ${GIT_COMMIT}${NC}"
echo -e "${YELLOW}Target: ${GOOS}/${GOARCH}${NC}"

go build -trimpath -ldflags "${LDFLAGS}" -o ./hl_monitor cmd/hl_monitor/main.go

if [ $? -eq 0 ]; then
    echo -e "${GREEN}Build succeeded: ./hl_monitor${NC}"
    ls -lh ./hl_monitor
else
    echo -e "${RED}Build failed${NC}"
    exit 1
fi

