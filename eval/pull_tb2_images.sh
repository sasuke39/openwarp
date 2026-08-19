#!/bin/bash
# 批量拉取 Terminal-Bench 2.0 任务镜像。
# 背景:Docker Hub 直连不可达,daocloud 对 alexgshaw/* 不在白名单,
# dockerproxy.net 可用。用法: ./pull_tb2_images.sh [并行数,默认 3]
set -uo pipefail

DATASET_DIR="$(cd "$(dirname "$0")" && pwd)/datasets/terminal-bench-2"
MIRROR="dockerproxy.net"
LOG="$(dirname "$0")/pull_tb2_images.log"
JOBS="${1:-3}"

images=$(grep -h '^docker_image' "$DATASET_DIR"/*/task.toml | sed -E 's/.*"([^"]+)".*/\1/' | sort -u)
total=$(echo "$images" | wc -l | tr -d ' ')
echo "共 $total 个镜像,并行 $JOBS,日志: $LOG"
: > "$LOG"

pull_one() {
  img="$1"
  if docker image inspect "$img" >/dev/null 2>&1; then
    echo "SKIP $img(已存在)" >> "$LOG"
    return 0
  fi
  if docker pull "$MIRROR/$img" >> "$LOG" 2>&1; then
    docker tag "$MIRROR/$img" "$img"
    docker rmi "$MIRROR/$img" >/dev/null 2>&1 || true
    echo "OK   $img" >> "$LOG"
    return 0
  fi
  echo "FAIL $img" >> "$LOG"
  return 1
}

export -f pull_one
export MIRROR LOG

echo "$images" | xargs -P "$JOBS" -I {} bash -c 'pull_one "$@"' _ {}

ok=$(grep -c '^OK' "$LOG" || true); skip=$(grep -c '^SKIP' "$LOG" || true); fail=$(grep -c '^FAIL' "$LOG" || true)
echo "完成: OK=$ok SKIP=$skip FAIL=$fail / $total"
grep '^FAIL' "$LOG" || true
