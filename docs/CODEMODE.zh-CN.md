# Code Mode（实验性，仅 Linux）

English: [CODEMODE.md](CODEMODE.md)。

Code Mode 从 **v0.2.2** 起作为 miodesk Core 的**可选实验性功能**提供，
默认关闭。它借鉴 Codex 的“代码组合工具”思路，但不是 Codex 的 V8
`exec/wait` 运行时；实验性工具 schema 与配额可能变化。

## 开启与关闭

在 `miodesk config` 指出的配置文件 `config.toml` 中添加：

```toml
[codemode]
enabled = true
```

然后**只重启 Core**。默认十个工具保持不变，开启后多一个只读
`code` 工具。Tunnel 不参与 JavaScript 执行，也不需要重启。
如果 ChatGPT 已缓存旧 schema，必须在 App 管理界面显式 Refresh
并检查新动作是否启用。

只支持 Linux；macOS/Windows 的预编译包可正常使用其他十个工具，
但明确开启 Code Mode 会被配置验证拒绝。

## JavaScript 示例

向 `code` 的 `source` 参数传一个返回 JSON 的 async arrow function：

```js
async () => {
  const hits = await tools.search({query: "TODO", path: "project", max_results: 10});
  const details = [];
  for (const hit of hits.matches) {
    const text = await tools.read({path: hit.file, offset: hit.line, limit: 1});
    details.push({file: hit.file, line: hit.line, content: text.content.trim()});
  }
  return details;
}
```

返回格式是 `{"kind":"code","value":...,"calls":N}`。
`value` 可为对象、数组、数字、字符串、布尔值或 null。
建议只返回**过滤后的少量信息**，而不是整份文件和全部搜索结果。

目前仅暴露 `tools.read`、`tools.search`（普通文字，非正则）和
`tools.list`。不能通过此 API 修改文件、执行 Shell、访问 Node.js、
网络接口、模块加载器或保持 JS 会话。工具仍由父 Core 校验工作区路径
并执行，JS Worker 无法直接获得这些内部实现。

## 资源与安全边界

- **独立进程：** 每次脚本在短生命周期的 JavaScript Worker 中执行，
  不是直接在 Core 主进程中。
- **Linux 资源限制：** `main` 已把每个 Worker 的
  `RLIMIT_DATA` 上限调整为 **256 MiB**；
  **已发布 v0.2.2 仍为 128 MiB**。禁用 core dump。
- **执行配额：** 每次最多 16 次内部工具调用；代码源码最多 16 KiB，
  中间响应累计最多 512 KiB，最终结果最多 32 KiB；
  JS 执行上限 2 秒，子进程整体截止 4 秒，最多并发 2 个 Worker。
- **工具权限：** 只能按白名单请求 Core 代理的只读调用；
  IPC 消息受大小和协议顺序限制。
- **环境：** Worker 不继承 Core 的敏感环境变量，
  工作目录保持中立；用户编写的 JS 异常正文不写入 Core 持久日志。

**特别重要：这里没有完整 OS 文件系统／容器沙箱。**
Worker 与 Core 使用同一 Unix 用户；底层 JS 引擎出现原生漏洞时，
不能将 API 白名单视为强内核安全边界。因此不建议让不可信的
第三方脚本直接使用 Code Mode，也不要以为启用它就能代替
`exec_command` 的正常授权边界。

## 验证与性能

仓库测试覆盖工具组合、只读权限、路径越界、资源超限、
死循环终止、MCP JSON 类型返回和异常日志隐私。

运行（在仓库中）：

```sh
go test ./internal/codemode
bash scripts/codemode-smoke/ci.sh
```

Code Mode 会多一次启动进程和内部消息通信的开销；
尚无足够的真实模型任务 A/B 数据可以证明整体更快或 token 更省。

如需了解安装及连接阶段的分工，请读
[Agent 安装指南](AGENT_SETUP.zh-CN.md)；
如果涉及操作权限，请读[安全模型](SECURITY.zh-CN.md)。
