# Terminal-Bench 2.0 评测接入层(warp-local-adapter)

链路:Harbor(宿主机)→ `eval.warp_local_agent` → HTTP → 本机 adapter(18889)→ LLM;
工具调用经 `tb_client` 落进任务容器执行。协议细节见 ../WARP_CLIENT.md 与
`tb_client/client.py` 头注释。

## 一次性准备

```bash
cd local-adapter/eval
python3 -m venv .venv && .venv/bin/pip install -r requirements.txt
PATH="$PWD/.venv/bin:$PATH" ./tb_client/gen_stubs.sh   # pb stub 已提交,仅 proto 变更后重跑
# harbor 的解释器需要 protobuf(requests 已自带),注入一次:
uv pip install --python ~/.local/share/uv/tools/harbor/bin/python protobuf
cp config.eval.yaml config.eval.local.yaml              # 填入真实 api_key(已被 gitignore)
```

## 每次评测

```bash
# 1) 启动 adapter(评测配置:端口 18889、memory 关闭;日志在 eval/warplocal.log)
../bin/warp-local-adapter -config config.eval.local.yaml &
# 2) 跑 Terminal-Bench 2.0(-l 题目数,-n 并发;PYTHONPATH 让 harbor 能导入自定义 agent)
cd .. && PYTHONPATH="$PWD" harbor run -d terminal-bench/terminal-bench-2 \
  -a eval.warp_local_agent:WarpLocalAgent -l 5 -y
```

可选覆盖:`--ak adapter_base_url=http://127.0.0.1:18889`、`--ak max_rounds=100`;
或环境变量 `WARP_LOCAL_ADAPTER_BASE_URL` / `WARP_LOCAL_MAX_ROUNDS` /
`WARP_LOCAL_CMD_TIMEOUT`(单命令同步等待秒数,默认 120)/
`WARP_LOCAL_OUTPUT_CAP`(单次工具结果字符上限,默认 60000)。

## 结果在哪看

- `jobs/<job-name>/results.json`:总分与每题 verdict;`jobs/<job-name>/<trial>/agent/`
  下的产物:`trajectory.json`(Harbor ATIF 轨迹,`harbor view` 直接展示)、
  `warp-local-events.jsonl`(增量事件流,超时/异常也不丢已写行)、
  `warp-local-transcript.json` / `warp-local-final.txt`(兼容产物)。
- 超时归因:agent 被 harbor cancel 时照样落盘以上文件;events.jsonl 最后一行
  是 cancelled/summary,trajectory.json 保留已完成的轮次。
- adapter 侧日志:`eval/warplocal.log`(每轮请求/工具/LLM 流)。

## smoke(不起 Harbor 的全链路自测)

```bash
.venv/bin/python -m unittest discover -s tb_client.tests   # 无网络单测
./smoke_local.sh   # adapter + alpine 小题;无 config.eval.local.yaml 时用 mock LLM
# 完整 trial 自测(harbor + 本地任务 + mock LLM,全程离线,预期 reward=1.0):
.venv/bin/python -m tb_client.mock_llm & ../bin/warp-local-adapter -config config.eval.mock.yaml &
cd .. && PYTHONPATH="$PWD" harbor run -p eval/testdata/hello-task \
  -a eval.warp_local_agent:WarpLocalAgent -y
```

## 常见坑

- **apply_file_diffs 基本不可用**:服务端 enforcePathPolicy 以 adapter 进程 CWD 为
  工作区,容器路径一律按"需确认"拒绝(不改 Go 代码无法放开)。模型被拒后会回退到
  run_shell_command 写文件,属预期行为;评测公平性不受影响。
- **memory 必须关**:协议不支持按请求传 memory base_dir;开启会让多任务共享
  /app → 同一 project key,跨任务污染。故 config.eval.yaml 里 `memory.enabled: false`。
- **上下文无服务端压缩**(随 memory 关闭):靠 OUTPUT_CAP 截断保护;别调太大。
- **长命令**:超过 CMD_TIMEOUT 未结束的命令转后台(nohup+日志文件),模型用
  read_shell_command_output 轮询;单次轮询封顶 180s。容器只有 sh/busybox 也能跑。
- **search_codebase 是 grep 近似**(无语义索引),先整串匹配、再按词 OR。
- **并发**:`-n` 并发 trial 共享一个 adapter;会话按 conversation_id 隔离,安全。
- **Windows 任务不支持**(执行器是 POSIX shell)。
- **任务镜像拉取**:TB2 任务镜像在 Docker Hub(如 alexgshaw/*),镜像站对个人
  namespace 普遍不放行。任务带 Dockerfile 时用 `harbor run --force-build` 本地
  构建绕过(官方 base 镜像如 python:* 可正常拉);无 Dockerfile 的任务需自行
  想办法拉预构建镜像。
