#!/bin/bash
# 一键评测:单 adapter + 每次运行一个时间戳记忆目录。
# 会话记忆开启(按 conversation_id 天然隔离),项目记忆关闭(避免跨题污染)。
# 用法: ./run_sample.sh [数据集目录,默认 datasets/sample9] [并发数,默认 10]
set -euo pipefail

cd "$(dirname "$0")/.."
EVAL_DIR="$PWD/eval"
DATASET="${1:-eval/datasets/sample9}"
CONCURRENCY="${2:-10}"
PORT="${EVAL_PORT:-18910}"
TS=$(date +%Y%m%d-%H%M%S)
MEM_DIR="$EVAL_DIR/mem/$TS"
mkdir -p "$MEM_DIR"

KEY=$(grep -E '^api_key:' "$HOME/Library/Application Support/WarpLocal/config.yaml" | sed 's/api_key: *//' | tr -d '"'"'"' \r')
if [ -z "$KEY" ]; then echo "未找到 api_key"; exit 1; fi

# 从用户当前应用配置动态读取模型/端点(不再硬编码)
USER_CFG="$HOME/Library/Application Support/WarpLocal/config.yaml"
PROVIDER=$(grep -E '^provider:' "$USER_CFG" | sed 's/provider: *//' | tr -d '"'"'"' \r')
BASE_URL=$(grep -E '^base_url:' "$USER_CFG" | sed 's/base_url: *//' | tr -d '"'"'"' \r')
MODEL=$(grep -E '^model:' "$USER_CFG" | sed 's/model: *//' | tr -d '"'"'"' \r')
echo "模型: $MODEL  端点: $BASE_URL"

CFG="$EVAL_DIR/config.run-$TS.yaml"
cat > "$CFG" <<EOF
provider: ${PROVIDER:-Custom}
base_url: ${BASE_URL}
model: ${MODEL}
max_tokens: 32768
api_key: $KEY
server:
  host: 127.0.0.1
  port: $PORT
memory:
  enabled: true
  base_dir: $MEM_DIR
  auto_enabled: false   # 项目记忆关闭;会话记忆默认开启
EOF

echo "记忆目录: $MEM_DIR"
echo "adapter: 127.0.0.1:$PORT  数据集: $DATASET  并发: $CONCURRENCY"

pkill -f "warp-local-adapter -config .*config.run-" 2>/dev/null || true
sleep 1
nohup bin/warp-local-adapter -config "$CFG" > "$MEM_DIR/adapter.log" 2>&1 &
for i in $(seq 1 10); do
  sleep 1
  if curl -s -m 2 "http://127.0.0.1:$PORT/health" | grep -q '"ok":true'; then break; fi
done
curl -s -m 3 "http://127.0.0.1:$PORT/health" || { echo "adapter 启动失败"; exit 1; }
echo

# 共享 uv/pip 缓存:verifier 每题都要装 uv+pytest,缓存跨 trial 复用。
# 注意:harbor 的 --mounts 只生成 services.main.volumes,不生成顶层 volumes 声明,
# 命名卷会报 "undefined volume",必须用 bind 挂载宿主机目录。
CACHE_DIR="$EVAL_DIR/cache"
mkdir -p "$CACHE_DIR/uv" "$CACHE_DIR/python" "$CACHE_DIR/pip"
MOUNTS='[
  {"type":"bind","source":"'"$CACHE_DIR/uv"'","target":"/var/cache/tbench/uv"},
  {"type":"bind","source":"'"$CACHE_DIR/python"'","target":"/var/cache/tbench/python"},
  {"type":"bind","source":"'"$CACHE_DIR/pip"'","target":"/var/cache/tbench/pip"}
]'

PYTHONPATH="$PWD" WARP_LOCAL_ADAPTER_BASE_URL="http://127.0.0.1:$PORT" \
  harbor run -p "$DATASET" -a eval.warp_local_agent:WarpLocalAgent \
  -n "$CONCURRENCY" --verifier-timeout-multiplier 3 \
  --mounts "$MOUNTS" \
  --ve UV_CACHE_DIR=/var/cache/tbench/uv \
  --ve UV_PYTHON_INSTALL_DIR=/var/cache/tbench/python \
  --ve PIP_CACHE_DIR=/var/cache/tbench/pip

echo ""
echo "=== 完成。记忆在 $MEM_DIR,轨迹在 jobs/ 最新目录 ==="
