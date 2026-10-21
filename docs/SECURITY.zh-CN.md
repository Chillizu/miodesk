# 安全模型（中文版）

English: [SECURITY.md](SECURITY.md)。

本文描述 miodesk 当前的安全边界。**连接任何拥有 Shell
权限的 MCP 客户端，相当于信任该客户端能以你自己的系统身份运行命令。**

## 文件工具与命令工具的区别

- `read`、`search`、`list`、`write`、`edit`、`delete`：
  仅操作配置的 `workspace.root`。通过规范化路径、解析符号链接等
  避免 `../`、绝对路径和符号链接逃逸。删除操作拒绝直接删除工作区根目录。
  大操作受深度、行数、字节数等上限约束。
- `exec_command`、`write_stdin`：命令的**起始工作目录**
  必须位于工作区内，但命令进程本身拥有当前系统用户的正常文件和
  网络权限，**不受工作区文件沙箱限制**。运行时间、输出字节数、
  会话数有上限；Unix 下可选 PTY。若干常见提权命令会被拒绝，
  但这不等于真正的 OS 权限隔离。

不要把工作区目录理解成能够限制任意 Shell 命令的安全沙箱；
对于不信任的模型、网页或外部脚本，应该在 OS 层面另行隔离。

## 实验性 Code Mode（仅 Linux）

`[codemode] enabled = true` 可增加第十一个只读 `code` MCP 工具；
默认关闭。它在单独的、资源受限的 Goja JavaScript Worker 中组合
`read/search/list`，由 Core 校验每个工具请求，不暴露写入和 Shell API。

`v0.2.3` 起 Worker Linux `RLIMIT_DATA` 上限为 **256 MiB**；
旧版 v0.2.2 为 **128 MiB**。每次最多执行 2 秒 JS，整体
请求限时 4 秒，最多两个并发 Worker，并有源码、消息、结果大小限制。

**这不是完整的 OS 文件系统或容器沙箱。** 子进程仍以和 Core
相同的 Unix 用户运行；JS 引擎层的原生漏洞可能绕过语言 API 白名单。
不要把不可信的第三方脚本交给此工具。
完整说明见 [CODEMODE.zh-CN.md](CODEMODE.zh-CN.md)。

## 访问模式

| 模式 | 访问范围 | 推荐用途 |
| --- | --- | --- |
| `local`（默认） | 本机 loopback／stdio | 本地开发与私有访问 |
| `token` | 带 Bearer token 的访问者 | 操作者自行管理的入口 |
| `unsafe` | 任何拥有 URL 的人 | 仅限明确授权的短期实验 |

`miodesk serve` 默认绑定 `127.0.0.1`；未配置认证时拒绝非 loopback
绑定，并校验浏览器 Origin 来限制 DNS rebinding 风险。

公开入口应该配置适当的认证，不要将 `remote.mode=unsafe`
当作 ChatGPT 连接失败时的修复办法。ChatGPT 的官方 Secure MCP
Tunnel 是**独立的出站连接组件**：只将请求转发到本机 Core，
不需要把本地监听端口开放到公网。

`token` 模式的 256-bit Bearer token 仅保存在用户受限的配置文件中，
使用常量时间比较；不通过 `status`、`doctor` 或诊断输出泄漏。
ChatGPT 自定义 MCP 的文档化认证方式可能与该模式不同，
应参考 OpenAI 官方文档与实际工作区设置。

## 密钥、权限与日志

- 不在聊天、命令参数、Git 仓库、截图或公共日志中粘贴
  OpenAI runtime key、Token 或私有配置内容。
- 官方 Tunnel profile 由外部 `tunnel-client` 维护；miodesk 不保存
  原始 runtime key，只保留密钥文件引用。Unix 使用 `chmod 600`，
  Windows 检查文件实际 ACL。
- 设置 Core／Tunnel systemd 服务前检查已有 `EnvironmentFile`
  和单位依赖，不覆盖旧的凭据来源。
- `/healthz` 不包含工作区数据；`/api/status` 暴露本地诊断元数据；
  `/api/edits` 可能含编辑过的文本，有内存保留限额及访问控制。
- HTTP／MCP 结构化日志包含请求 ID、耗时、状态；
  不应包含请求体、文件正文、命令内容或 Authorization 头。
  用户控制的 Code Mode JS 异常文本不会被复制进 Core 持久日志。
- `miodesk logs` 进一步做 token 脱敏；需要排查时只分享必要的、
  脱敏后的错误与检查结果。

## 事件报告

发现安全漏洞时请使用 [GitHub 私密漏洞报告](https://github.com/Chillizu/miodesk/security/advisories/new)，**不要公开漏洞利用细节、密钥或敏感日志**。一般使用问题可提交[普通 Bug 反馈](https://github.com/Chillizu/miodesk/issues/new?template=bug_report.yml)。

更多：[安装引导](AGENT_SETUP.zh-CN.md)、
[Core/Tunnel 设备配置](SETUP.zh-CN.md)、[英文安全原文](SECURITY.md)。
