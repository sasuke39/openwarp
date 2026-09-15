# MCP 授权策略持久化

## 问题

旧版把 policy.json 和 tools.sock 一起放在系统临时目录。
策略文件消失后，App 仍监听 Socket，但所有请求读取策略失败。
只重连 Codex 无法修复缺失的授权文件。

## 实现

- 默认策略位于客户端 data_dir/tool-ipc/policy.json（Local 为 ~/.warp-local）。
- tools.sock 保持原临时路径，Codex MCP 配置无需改变。
- 首次启动新版时，只迁移已有、当前用户所有且权限私有的合法旧策略。
- 持久化策略存在时不覆盖，保留撤销授权和禁用操作。
- 不从 SSH Profile 列表推断授权，不自动补全 server_ids 或 transfer_roots。
- 迁移使用同目录临时文件及禁止覆盖的原子发布。
- 显式 WARPLOCAL_TOOL_IPC_DIR 保持原语义，策略与 Socket 都在指定目录。
- 每次请求仍重新读取策略，撤销立即生效；新增授权仍需 App 重启。
- 缺失策略的错误包含策略路径。

## 发布与验证

- 需要重新构建客户端并重启后启用新路径；仅改源码不影响运行中的 App。
- 回归覆盖迁移、保留禁用状态、清理 runtime 后策略仍可读。
- scripts/test-mcp-local-ssh.sh 验证真实本地 SSH 和客户端 MCP 执行链路。
- 本改动不修改 Agent Harness 协议或 Turn 生命周期。
- 旧临时策略留给旧版客户端兼容；新版读取持久化策略。
