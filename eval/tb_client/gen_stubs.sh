#!/usr/bin/env bash
# 从 local-adapter/proto3 生成 Python protobuf stub 到 tb_client/pb/。
# 产物随仓库提交,proto 变更后才需要重跑。依赖:pip install grpcio-tools。
set -euo pipefail
cd "$(dirname "$0")"
PROTO_DIR="../../proto3"
mkdir -p pb
python3 -m grpc_tools.protoc -I"$PROTO_DIR" --python_out=pb "$PROTO_DIR"/*.proto
echo "generated $(ls pb/*_pb2.py | wc -l | tr -d ' ') modules into tb_client/pb/"
