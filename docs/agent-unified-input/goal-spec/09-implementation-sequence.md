# 实施顺序

每阶段独立提交、测试通过后再进入下一阶段。禁止一次性大改 UI、Adapter 和 SSH。

## Phase 0：保护当前版本

- 记录客户端与 Adapter HEAD、当前 App 校验值和构建方式。
- 建立可回退分支/tag；确认两个工作树无未识别改动。
- 保留原型、截图和 fixture。

## Phase 1：只读视觉壳

- 建立视觉 token 和统一输入布局。
- 用 fixture 渲染 Terminal/Agent 两张状态图。
- 不改变提交、执行、Queue、Steer 逻辑。
- 完成像素回归后才接业务。

## Phase 2：输入快照

- 增加 revision、route、source、context 快照。
- 修正中文路径和已知命令优先级。
- 接通自动/手动模式展示、焦点和键盘操作。

## Phase 3：提交策略

- Shell 路径不再取消 Agent Turn。
- Agent 路径按状态 start/queue/delay。
- Queue 转 Steer 接通 accepted/applied/failed。
- Stop 后验证下一 Turn。

## Phase 4：统一时间线

- 混排 Terminal Block、Agent Rich Content 和 Pending Prompt。
- 补 Conversation visibility、稳定排序和恢复。
- 验证长历史滚动性能及位置保持。

## Phase 5：SSH

- Warpified SSH 接结构化 Block。
- 兼容 SSH 接上下分屏和明确提示。
- 验证独立 background channel、Keychain 和拖拽上传。

## Phase 6：收口

- 删除重复旧分支和废弃 UI。
- 跑全量、真实本机 SSH、视觉和人工测试。
- 填写实施记录，合入 main，构建并覆盖 App。
