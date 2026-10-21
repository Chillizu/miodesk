# 设备部署指南（中文版）

English: [SETUP.md](SETUP.md)。如果你希望由普通 ChatGPT 一步一步
引导安装，请使用 [Agent 安装指南](AGENT_SETUP.zh-CN.md)。

## 1. 安装与系统检查

正式发行的预编译包支持 Linux x64/ARM64、macOS x64/ARM64 和
Windows x64。普通安装**不需要** OpenAI Platform 账户或 Tunnel。

Linux/macOS 使用 [install.sh](../scripts/install.sh)，Windows x64
使用 [install.ps1](../scripts/install.ps1)。两者均验证 GitHub Release
的 SHA-256；Windows 原生安装包不包含 systemd，也不会修改系统启动项。

查看系统类型：

```sh
uname -s
uname -m
```

Windows PowerShell：

```powershell
[System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture
```

安装完成后：

```sh
miodesk version
miodesk help
```

在较新的开发版本中，`miodesk preflight --json` 是无需工作区配置的只读
平台探测；**v0.2.2 尚未提供这个命令**。请先用 `miodesk help`
判断是否支持。

## 2. 选定工作区并配置 Core

工作区应为用户自己选择、**已经存在的可信项目目录**。文件工具严格
限制在工作区内，但 Shell 命令仍以当前用户身份运行，不能因此把
它当作完整的隔离环境。

```sh
cd /absolute/path/to/project
miodesk setup --workspace "$PWD"
miodesk doctor --json
```

Windows PowerShell：

```powershell
$miodesk = "$env:LOCALAPPDATA\Programs\miodesk\miodesk.exe"
& $miodesk setup --workspace "C:\path\to\project"
& $miodesk doctor --json
```

所有明确传入 `--workspace` 的 `setup` 都不进入交互向导；
开发版本还提供 `--non-interactive` 明确禁止提问。
新用户不要使用 `init` 的默认 home workspace 直接提供给不信任的 Agent。

Core 默认端口是 `127.0.0.1:8787`。Tunnel 需要固定端口，
`--port 0` 只适合本地临时测试。改变已有工作区根目录需要
`--force`，不会在已有配置上无提示重置。

保持前台服务运行：

```sh
miodesk serve
```

第二个终端运行 `miodesk status`，确认
`http://127.0.0.1:8787/healthz` 可访问。此时不需要启动 Tunnel，
也**不等于 ChatGPT 已经能调用工具**。

本地 stdio 客户端可直接使用 `miodesk serve --stdio`，无需额外
HTTP 服务；详见 [README](../README.zh-CN.md)。

## 3. 通过 OpenAI Secure MCP Tunnel 连接 ChatGPT（可选）

这是 ChatGPT 场景推荐的**私有连接**，不是 miodesk Core 的依赖。

实时官方资料：
[OpenAI Secure MCP Tunnel](https://developers.openai.com/api/docs/guides/secure-mcp-tunnels)
与 [ChatGPT 开发者模式／MCP 应用](https://help.openai.com/zh-hans-cn/articles/12584461-creating-custom-mcp-apps-in-chatgpt)。

需要准备：

1. 在 OpenAI Platform 的 Tunnel 设置中创建 `tunnel_id`，
   把它关联到正确的 Platform organization 和 ChatGPT workspace；
   创建／修改需要 Tunnels Read + Manage，使用 Tunnel 需要
   Tunnels Read + Use，ChatGPT 自定义 MCP 访问权限另行决定。
2. 从官方来源安装 `tunnel-client`，先检查
   `tunnel-client help quickstart` 的当前参数。
3. 首次配置最省事的做法是使用
   [Agent 指南中的单块交互式凭据命令](AGENT_SETUP.zh-CN.md#3-chatgpt-专属官方-secure-mcp-tunnel)：
   在**本机终端**询问 Tunnel ID，隐藏输入 runtime key，并自动创建
   私有文件及调用已有 `miodesk setup`。不需要把密钥文本发给 ChatGPT。
   Unix 文件权限为 0600；Windows 先限制当前用户 ACL，再写入密钥。
   原有的 `miodesk setup --interactive` 向导**继续支持**，适合已有
   私有密钥文件的用户（它询问的是路径，而非密钥正文）。

如果已经创建了密钥文件，可使用下列手动配置方式：

```sh
miodesk setup \
  --tunnel-id tunnel_YOUR_ID \
  --runtime-key-file /absolute/path/to/runtime-key
miodesk doctor
tunnel-client doctor --profile miodesk --explain
```

miodesk 保存 tunnel ID 和**密钥文件的路径**，不会把原始密钥复制到
config.toml；实际 profile 由官方 `tunnel-client` 生成。若失败，
优先看本机无敏感信息的错误与客户端帮助，避免手动发明 YAML 格式。

启动方式二选一：

- **两个前台窗口**：Core 运行 `miodesk serve`；
  第二个窗口运行 `tunnel-client run --profile miodesk`。
- **一个前台兼容命令**：确认没有已运行的 Core 和 Tunnel 后，
  单独执行 `miodesk connect` 同时启动两者。

官方客户端需要向 OpenAI 发起出站 HTTPS 请求，
本地 `8787` 不需要公开暴露。待客户端 ready、持续轮询后，
到 ChatGPT Plugins 中添加 **private custom MCP server**，
Connection 选 **Tunnel**，关联对应 tunnel ID，并检查权限和风险提示。

**连接最终验收：** 当前 ChatGPT 对话中，真实的 `status`
工具调用必须成功；Tunnel 在线和本地 `/healthz` 正常都不够。

## 4. 常驻系统服务（只有连接验证通过后才考虑）

Linux/systemd 可分别安装：

```sh
miodesk service install
miodesk service start
miodesk tunnel service install
miodesk tunnel service start
```

这会产生**独立**的 `miodesk.service` 和
`miodesk-tunnel.service`，互相不应因 Core 重启而连带重启。

**不能直接覆盖旧配置：** 先运行
`systemctl --user cat miodesk.service miodesk-tunnel.service`，
核对 `Requires`、`PartOf`、`Wants`、
`EnvironmentFile`、现有登录凭据与 profile。
miodesk 不应覆盖依赖外部密钥环境文件的 Tunnel Unit。
确需迁移时，安排维护窗口并在用户授权下修改 systemd drop-in，
然后重新核对两个服务的 PID 与状态。不要默默开启 linger。

Windows/macOS 没有内置 miodesk systemd 服务管理器，
首次使用可以保留前台终端。只有用户明确需要时，才讨论对应
操作系统的计划任务、登录项或其他外部进程管理方案。

## 5. ChatGPT 接通之后可以交给 miodesk 做什么？

有了真正可调用的工具，ChatGPT 才能通过 `@miodesk`
查看项目文件、搜索日志、编辑工作区内配置、运行测试、
检查 `status` 和协调后续维护。每一步仍要遵守相应操作授权，
尤其 `exec_command` 具有当前系统用户权限。

默认关闭的 Linux [Code Mode（实验性）](CODEMODE.zh-CN.md)
可以在连接后单独讨论；它不是首次安装必需的。

## 6. 常见失败

| 现象 | 检查 |
| --- | --- |
| 找不到 `miodesk` | 运行安装器打印的绝对路径或检查 PATH |
| 工作区不正确 | 运行 `miodesk workspace`，必要时有意识地使用 `--force` |
| 本机端口占用 | 运行 `miodesk status`，不要再次启动相同 Core |
| 缺少 tunnel ID / 权限 | 检查 Platform 组织、workspace 关联和 Tunnels 权限 |
| 密钥文件不安全 | Unix 用 `chmod 600`，Windows 核对 ACL |
| Tunnel 不 ready | `tunnel-client doctor --profile miodesk --explain` |
| ChatGPT 不显示工具 | 检查自定义 MCP App 权限、Refresh 和新动作启用状态 |
| Core 服务异常 | `miodesk doctor`、`miodesk logs --follow --json` |

配置文件路径由 `miodesk config` 显示，运行日志默认不记录密钥或
文件正文。对于复杂旧 systemd Unit 迁移，可查阅
[英文 SETUP.md](SETUP.md#4-start-the-connection) 中的 drop-in 示例；
修改前应实际阅读旧 Unit，不能照抄覆盖。

更多：[ChatGPT 连接说明](CHATGPT.md)、
[安全边界](SECURITY.zh-CN.md)、[README 中文](../README.zh-CN.md)。
