# 交付运行手册

## 开发前

- 读取工作区 `AGENTS.md` 和本目录全部文档。
- 检查两个仓库状态，不覆盖用户改动。
- 从最新 main 建安全英文分支。
- 记录当前 `/Applications/WarpLocal.app` 版本和可恢复副本。

## 每阶段

- 先写/更新失败测试，再做最小实现。
- 修改前端时运行相关 Rust 测试和 fixture 截图。
- 修改 Adapter/工具/SSH 时必须运行真实本机 SSH 测试。
- 只提交本阶段文件；提交信息说明行为，不写模糊“fix”。
- 阶段结束更新 `12-implementation-record.md`。

## 合入前

- 更新 main 后解决冲突并重跑全部测试。
- 检查没有运行时配置、密钥、日志、缓存和构建产物进入 Git。
- 检查没有重复实现、死代码和被遗忘的 feature gate。
- 对比两张视觉 golden，人工检查 Terminal/Agent/SSH 三种页面。

## 构建覆盖

- 使用项目既有构建/部署脚本，不另造临时发布流程。
- 构建成功后检查 App bundle、可执行文件、Sidecar 和签名状态。
- 完全退出正在运行的 App，再做可恢复覆盖。
- 启动后确认 Adapter 健康、设置可读、旧 Profile 和历史存在。

## 发布冒烟

- 本地终端：Shell、Agent、Queue、Steer、Stop、下一 Turn。
- SSH：Warpify 成功和兼容模式各一次。
- 后台 job：启动、读输出、停止，用户终端同时可用。
- 文件拖拽：复用钥匙串上传当前远端目录。
- App 重开：Conversation、Block 和 Steer 历史恢复。

## 回退

出现崩溃、无法登录、历史丢失、SSH 失效或 Turn 卡死立即停止发布，恢复原 App；不得用连续覆盖构建掩盖问题。记录失败提交、日志位置和复现步骤后再修复。
