#!/usr/bin/env bash
# 全链路 smoke:真实 adapter + alpine 容器 + 自造小题。
# 用法:
#   ./smoke_local.sh          有 config.eval.local.yaml(真实 key)就走真实 LLM,
#                             否则自动改用本机 mock LLM(无需 key)。
set -euo pipefail
cd "$(dirname "$0")"

VENV=.venv
PORT=18889
PIDS=()

cleanup() { for p in "${PIDS[@]:-}"; do kill "$p" 2>/dev/null || true; done; }
trap cleanup EXIT

[ -d "$VENV" ] || { echo "先建 venv: python3 -m venv .venv && .venv/bin/pip install -r requirements.txt"; exit 1; }
ls tb_client/pb/request_pb2.py >/dev/null 2>&1 || PATH="$PWD/$VENV/bin:$PATH" ./tb_client/gen_stubs.sh

if [ -f config.eval.local.yaml ]; then
    CONFIG=config.eval.local.yaml
else
    echo "[smoke] 无 config.eval.local.yaml,改用 mock LLM(127.0.0.1:18999)"
    "$VENV/bin/python" -m tb_client.mock_llm --port 18999 &
    PIDS+=($!)
    CONFIG=config.eval.mock.yaml
    sleep 1
fi

if curl -sf "http://127.0.0.1:$PORT/health" >/dev/null 2>&1; then
    echo "[smoke] adapter 已在 $PORT 端口运行,直接复用(注意确认它指向的 LLM)"
else
    echo "[smoke] 启动 adapter(../bin/warp-local-adapter -config $CONFIG)"
    ../bin/warp-local-adapter -config "$CONFIG" &
    PIDS+=($!)
    for i in $(seq 1 30); do
        curl -sf "http://127.0.0.1:$PORT/health" >/dev/null 2>&1 && break
        sleep 1
        [ "$i" = 30 ] && { echo "adapter 未就绪,看 eval/warplocal.log"; exit 1; }
    done
fi

"$VENV/bin/python" -m tb_client.smoke_e2e --adapter "http://127.0.0.1:$PORT"
