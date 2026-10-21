# Context：持久化工作上下文

**简体中文** · [English](CONTEXT.md) · [中文 README](../README.zh-CN.md)

`context` 是 miodesk 的 MCP 工具，用于保存一条长期工作线的**精简、结构化交接状态**。后续 ChatGPT 会话或其他兼容的 MCP 客户端，可以通过相同的 ID 获取当前目标、进度、重要决策、未解决问题和下一步操作，而不必重放之前的聊天记录。

它**不是**聊天记录数据库、模型隐藏推理的存储、自动记忆系统、任务调度器或 Git 式版本控制。必须由客户端明确调用 `context`；无状态的 MCP HTTP 连接无法自行判断哪个聊天会话拥有哪份上下文。

## 核心概念

- **上下文 ID**：跨聊天、跨兼容客户端标识一项长期工作。
- **活动交接状态（Active Handoff）**：可修改的最新状态，由 `update` 更新、由 `resume` 读取。
- **历史修订（Revisions）**：发生有效更新后，保留更新前的活动状态。
- **检查点（Checkpoints）**：为重要里程碑生成不可变快照；创建检查点不会回滚活动状态。
- **上下文列表**：`list` 按最后更新时间由新到旧列出已保存的工作线，不返回完整历史。

建议每个持续性项目或工作流使用稳定的 ID（例如 `miodesk-maintenance`），而不是每条聊天消息创建一个 ID。

## 四种操作

| `action` | 必要参数 | 行为 |
| --- | --- | --- |
| `update` | `id`；新建时还需有效内容 | 新建或增量更新活动状态。仅真实内容发生变化时递增 `revision`。 |
| `checkpoint` | `id`；已存在的状态或初始化内容 | 可先更新活动状态，再创建不可变里程碑快照；`label` 可选。 |
| `resume` | `id` | 获取最新活动状态、修订号和检查点数量，**不**返回所有历史快照。 |
| `list` | `action` | 列出精简上下文摘要，按 `updated_at` 降序排列（时间相同时按 ID 升序）。 |

`action` 忽略首尾空白和大小写。`id` 应符合正则 `^[a-z0-9][a-z0-9._-]{0,63}$`：长度 1–64，使用小写英文字母、数字、点、下划线和连字符，首字符为字母或数字。建议传入不带首尾空白的 ID。

## 请求字段

| 字段 | 类型 | 含义 |
| --- | --- | --- |
| `action` | string | `update`、`checkpoint`、`resume` 或 `list`。 |
| `id` | string | 稳定的工作线标识；除 `list` 外必须提供。 |
| `working_directory` | string | 工作区内已经存在的目录；只是工作定位信息，**不会**改变 `workspace.root` 或 Shell 的当前目录。 |
| `summary` | string | 当前交接内容的简短概述。 |
| `goal` | string | 这条工作线的目标。 |
| `current_state` | string | 已完成和尚在进行的工作。 |
| `approach_summary` | string | 当前方法与选择理由的简要总结，**不是**模型私有思维过程。 |
| `decisions` | string[] | 应传递到后续会话的重要决策。 |
| `open_questions` | string[] | 待解决的问题或阻碍。 |
| `next_steps` | string[] | 按顺序排列的下一步行动。 |
| `clear_fields` | string[] | 在 `update` / `checkpoint` 中显式清空指定标量字段。 |
| `label` | string | `checkpoint` 的可选标签。 |

传入 `working_directory` 时，miodesk 会将其解析到当前配置的工作区中，验证其确实存在且是目录。存储的是绝对形式的展示路径；安全的符号链接或 bind mount 路径会尽量保留调用者熟悉的路径形式。

### 增量更新规则

- 对 `summary`、`goal`、`current_state`、`approach_summary` 这几个字符串字段，只有**去除首尾空白后非空**的值才会替换旧值。省略字段或传入空字符串都不会清空旧内容。
- 对 `decisions`、`open_questions`、`next_steps` 数组，**省略**表示保留旧值；明确传入 `[]` 才会清空。数组内容会去除首尾空白、过滤空项并去重，同时保持顺序。
- 清除字符串字段或 `working_directory` 时，请使用 `clear_fields`，可选字段为 `working_directory`、`summary`、`goal`、`current_state`、`approach_summary`。如果同一次调用既赋值又清除，**清除优先**。`resume`、`list` 不允许使用它。
- 已存在的上下文在 `update` 后若没有实质变化，`revision` 和 `updated_at` 不变。首次创建必须包含有效交接内容。
- `checkpoint` 可以先应用更新。即使内容不变，也可以新增检查点并更新 `updated_at`，但不会增加 `revision`。
- 旧字段 `active_reasoning` 仅作为 `approach_summary` 的兼容别名保留。新调用请使用 `approach_summary`；二者同时出现时新字段优先。未知字段会被拒绝。

## MCP 调用示例

以下 JSON 是原生 `context` MCP 工具的**入参**，不是终端命令。

**1. 保存工作状态：**

```json
{
  "action": "update",
  "id": "example-maintenance",
  "working_directory": "my-project",
  "summary": "Core 升级已经验证，剩余文档工作。",
  "goal": "安全完成本次维护版本。",
  "current_state": "测试已通过，发布说明待编写。",
  "approach_summary": "Core 和可选 Tunnel 独立维护。",
  "decisions": ["只升级 Core 时不重启 Tunnel。"],
  "open_questions": ["客户端是否需要刷新工具 Schema？"],
  "next_steps": ["编写发布说明", "检查差异", "发布"]
}
```

`my-project` 是示例名称，必须替换成当前工作区内**已存在的目录**。不需要目录定位时可以不传 `working_directory`。

**2. 保存里程碑：**

```json
{
  "action": "checkpoint",
  "id": "example-maintenance",
  "label": "validation-passed",
  "current_state": "发布说明已完成，测试通过。"
}
```

**3. 发现已保存的工作线：**

```json
{"action": "list"}
```

**4. 在新会话恢复工作：**

```json
{"action": "resume", "id": "example-maintenance"}
```

恢复后仍需核对当前文件、Git 状态和实际服务情况。`next_steps` 是历史交接记录，不是实时检测结果。

## 返回数据结构

所有响应都包含 `kind: "context"` 与操作名 `action`：

- `update` / `resume`：返回 `context`（活动状态）。
- `checkpoint`：返回 `context` 和新建的 `checkpoint`。
- `list`：返回 `contexts`（精简摘要数组；没有记录时为 `[]`）。

活动 `context` 包含 `id`、可选的 `working_directory`、`state`（上述七个语义字段）、`revision`、`checkpoint_count`、`created_at` 和 `updated_at`。`list` 摘要包含 `id`、可选的 `working_directory` / `goal` / `current_state`、`revision`、`checkpoint_count` 和 `updated_at`。检查点还包含 `id`、可选的 `label` / `working_directory`、`created_at`、`revision` 和 `state`。

## 存储、安全与限制

- 每个 ID 对应一个 JSON 文件：`$XDG_DATA_HOME/miodesk/contexts/<id>.json`；默认位置是 `~/.local/share/miodesk/contexts/<id>.json`。数据保存在**项目仓库和 config.toml 之外**。
- Unix 上目录权限为 `0700`，新建文件为 `0600`；通过临时文件写入、同步和重命名替换目标文件。
- 每条工作线维护一个当前活动状态，并最多保留 **20 个历史修订**和 **50 个检查点**；超过上限自动淘汰最早的记录，检查点编号继续递增。
- 公开 MCP 接口**尚无** `history`、`get_checkpoint`、`rollback` 或 `delete` 操作。磁盘上留存历史不代表 `resume` / `list` 能读取完整历史。
- 数据可以跨 ChatGPT 会话及本机 Core 常规重启保留，但必须连接**同一台机器上的同一个数据目录**。它不会自动跨设备同步，也不会自动提交到 GitHub。
- 单个 Core 进程内部对存储操作使用互斥锁，但它不是分布式数据库，也不提供跨进程协调或锁定保证。
- 不要写入密钥、API Token、整份私密日志或不必要的个人信息。对历史交接内容也应像对普通外部数据一样核查后再执行。

## 实际使用建议

完成一次有意义的工作进展、形成关键决策或发现阻碍时，使用 `update`。在验收通过、发布完成等重要节点使用 `checkpoint`，不需要每执行一条命令就创建快照。如果忘记了 ID，先 `list` 再 `resume`。

实时连接诊断属于单独的 `status` 工具。关于权限边界，请参阅[安全模型](SECURITY.zh-CN.md)。完整实现位于 [`internal/contextstore/store.go`](../internal/contextstore/store.go)。
