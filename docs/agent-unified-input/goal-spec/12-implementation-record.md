# 实施记录（执行 Goal 时填写）

## 基线

- Warp 源码 HEAD：`e3a53ac58bfb6018ca5f1eb6d09827d0447c5fca`
- Adapter HEAD：`41483542d04711c36213f7b422f398bfc66eed67`
- 开发分支：两个仓库均为 `feature/20260829-unified-agent-terminal-input`
- 原 App 版本/校验值：`0.2.0` / `8971ef54111b9017f7f56beb8c573c64a1a52b3b14da795d41864c673528792e`
- 构建命令来源：待 Phase 0 从仓库既有脚本确认

## 实际实现

| 阶段 | 修改文件 | 实现说明 | 提交 |
|---|---|---|---|
| 视觉壳 | | | |
| 输入路由 | | | |
| 提交策略 | | | |
| 时间线 | | | |
| SSH | | | |
| 收口 | | | |

## 状态与协议变化

- 新增/修改的前端状态：
- 新增/修改的 Adapter API：
- 数据迁移或兼容处理：
- 删除的旧实现：

## 测试证据

- Rust 测试与结果：
- Go 测试与结果：
- Pi/DSH 测试与结果：
- 本机真实 SSH 测试与结果：
- 视觉对比工具、阈值和结果：
- 人工冒烟场景与结果：

## 视觉结果

- Terminal golden 路径/差异率：
- Agent golden 路径/差异率：
- 其他尺寸截图：
- 无障碍/焦点检查：

## 已知问题

必须写“无”或列出问题、严重级别和处理状态。存在未解决 P0/P1/P2、跳过测试或不明确项时禁止标记 Goal 完成。

## 交付

- 合入 main 提交：
- 推送结果：
- App 构建产物：
- 覆盖时间：
- 回退包位置：`~/Library/Application Support/WarpLocal/rollback/20260829-pre-unified-input/WarpLocal.app`
- 最终确认人/时间：
