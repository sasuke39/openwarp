#!/bin/bash
# 给所有 alexgshaw/* 镜像预装 uv + curl + pip(验证器依赖),避免判分时网络下载。
# 原理:docker create -> docker commit 预装 -> retag 回原名。
# 用法: ./preinstall_deps.sh
set -euo pipefail
cd "$(dirname "$0")/.."

images=$(docker images --format '{{.Repository}}:{{.Tag}}' | grep '^alexgshaw/' | grep ':20251031$')
total=$(echo "$images" | wc -l | tr -d ' ')
done=0; fail=0
echo "共 $total 个镜像待处理"

for img in $images; do
  cid=$(docker create --platform linux/amd64 "$img" sleep 1 2>/dev/null) || { echo "FAIL create $img"; fail=$((fail+1)); continue; }
  # 启动容器,装依赖,停掉
  docker start "$cid" >/dev/null 2>&1
  docker exec "$cid" sh -c '
    export DEBIAN_FRONTEND=noninteractive
    # curl 大部分镜像已有,确保装上
    which apt-get >/dev/null 2>&1 && apt-get update -qq >/dev/null 2>&1 && apt-get install -y -qq curl python3-pip >/dev/null 2>&1
    # 预装 uv(验证器几乎都要用)
    if ! command -v uvx >/dev/null 2>&1; then
      curl -LsSf https://astral.sh/uv/0.9.5/install.sh 2>/dev/null | sh >/dev/null 2>&1 || true
      ln -sf /root/.local/bin/uv /usr/local/bin/uv 2>/dev/null || true
      ln -sf /root/.local/bin/uvx /usr/local/bin/uvx 2>/dev/null || true
      # 创建 env wrapper(部分验证器直接调 /root/.local/bin/env)
      mkdir -p /root/.local/bin
      cat > /root/.local/bin/env <<ENVEOF
#!/bin/sh
exec "\$@"
ENVEOF
      chmod +x /root/.local/bin/env 2>/dev/null || true
    fi
    # 预装 pytest + 插件(验证器常用)
    pip3 install -q pytest==8.4.1 pytest-json-ctrf==0.3.5 2>/dev/null || true
  ' 2>/dev/null
  docker stop "$cid" >/dev/null 2>&1
  docker commit --change "ENV HTTP_PROXY=" --change "ENV HTTPS_PROXY=" --change "ENV NO_PROXY=" "$cid" "$img" >/dev/null 2>&1 && {
    echo "OK $img"; done=$((done+1))
  } || { echo "FAIL commit $img"; fail=$((fail+1)); }
  docker rm "$cid" >/dev/null 2>&1
done

echo "完成: OK=$done FAIL=$fail / $total"
