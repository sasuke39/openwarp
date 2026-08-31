# 组件与代码映射

## 复用优先

| 原型区域 | 现有组件/文件 | 策略 |
|---|---|---|
| 页面和滚动 | `TerminalView`、`BlockListElement` | 复用 |
| 终端命令 | `Terminal Block` | 复用 |
| Agent 内容 | `AIBlock`、Rich Content | 复用 |
| 输入编辑器 | `terminal/input.rs` | 复用 |
| 模式切换 | `universal_developer_input.rs` | 改样式/文案 |
| 自动识别 | `blocklist/input_model.rs` | 修正路由快照 |
| 排队提示 | `pending_user_query_block.rs` | 扩展状态 |
| Steer 投递 | `view/pending_user_query.rs` | 复用 API |
| Block 可见性 | `terminal/model/block.rs` | 补持久化测试 |
| SSH 命令 | Session executor/transport PTY | 分能力路由 |

## 建议新增的前端类型

```text
InputRouteSnapshot
  revision
  text
  route: terminal | agent
  source: automatic | manual | explicit_prefix
  context: host/profile + user + cwd + shell + conversation
```

```text
UnifiedComposerPresentation
  route label
  environment chips
  submit hint
  agent admission state
```

只新增小型状态对象，不新增第二套输入框、消息列表或进程管理器。

## 建议文件组织

- 输入快照和策略放在 `app/src/terminal/input/` 下的独立小文件。
- 视觉 token 放在统一输入组件附近，组件只引用语义字段。
- Queue/Steer 展示继续放在 Pending User Query Block。
- Conversation/Turn 生命周期继续由 Adapter 和现有 Controller 管理。

## 禁止做法

- 不把原型 HTML 嵌入客户端。
- 不复制 Terminal Block 生成假聊天卡片。
- 不在多个 UI 文件分别维护 Turn 状态判断。
- 不把 SSH transport 命令或环境变量展示给用户。
- 不保留新旧两套重复实现；替换完成后删除废弃分支。
