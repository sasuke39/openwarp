#!/bin/bash
# 从 ghcr.io/sasuke39/tb2 全速拉回 TB2 镜像并 retag 成 alexgshaw 原名。
# 用法: ./pull_from_ghcr.sh [镜像列表文件,默认 gha_images.txt]
set -uo pipefail

cd "$(dirname "$0")"
LIST="${1:-gha_images.txt}"
OWNER="sasuke39"
LOG="pull_from_ghcr.log"
: > "$LOG"

GHCR_MIRROR="${GHCR_MIRROR:-ghcr.nju.edu.cn}"
pull_one() {
  img="$1"  # 形如 path-tracing:20251031
  src="$GHCR_MIRROR/$OWNER/tb2/$img"
  dst="alexgshaw/$img"
  if docker image inspect "$dst" >/dev/null 2>&1; then
    echo "SKIP $img" >> "$LOG"; return 0
  fi
  if docker pull "$src" >> "$LOG" 2>&1; then
    docker tag "$src" "$dst"
    docker rmi "$src" >/dev/null 2>&1 || true
    # 注入容器代理(评测环境验证器下载走 7892 代理)
    cid=$(docker create "$dst" 2>/dev/null) && \
      docker commit --change "ENV HTTP_PROXY http://host.docker.internal:7892" \
                    --change "ENV HTTPS_PROXY http://host.docker.internal:7892" \
                    --change "ENV NO_PROXY localhost,127.0.0.1" "$cid" "$dst" >> "$LOG" 2>&1 && \
      docker rm "$cid" >/dev/null 2>&1
    echo "OK   $img" >> "$LOG"; return 0
  fi
  echo "FAIL $img" >> "$LOG"; return 1
}

export -f pull_one
export OWNER LOG GHCR_MIRROR

xargs -P 6 -I {} bash -c 'pull_one "$@"' _ {} < "$LIST"
ok=$(grep -c '^OK' "$LOG" || true); skip=$(grep -c '^SKIP' "$LOG" || true); fail=$(grep -c '^FAIL' "$LOG" || true)
echo "完成: OK=$ok SKIP=$skip FAIL=$fail"
grep '^FAIL' "$LOG" || true
