#!/bin/sh
# 验证 /work/hello.txt 内容,reward 写到 /logs/verifier/reward.txt
mkdir -p /logs/verifier
if [ "$(cat /work/hello.txt 2>/dev/null)" = "hello terminal-bench" ]; then
  echo 1 > /logs/verifier/reward.txt
  echo "PASS"
else
  echo 0 > /logs/verifier/reward.txt
  echo "FAIL: $(cat /work/hello.txt 2>/dev/null || echo missing)"
fi
