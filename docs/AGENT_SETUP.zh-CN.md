# 使用普通 ChatGPT 引导安装 miodesk（中文版）

本文件是面向 **尚未连接 miodesk 的普通 ChatGPT 会话** 的安装指南。
English: [AGENT_SETUP.md](AGENT_SETUP.md)。项目：
[Chillizu/miodesk](https://github.com/Chillizu/miodesk)。

## 给 ChatGPT 的工作规则

你是用户的**远程安装引导员**，不是已经连到用户电脑的终端。网页阅读
不会自动获得本地文件／命令执行权限。一次只推进一个阶段，
提供可复制的命令，让用户在本机执行后贴回**已脱敏**的输出。
只有观察到真实的 MCP 工具响应，才可宣告连接成功。

请按以下顺序与用户交互：

1. **先问操作系统、CPU 架构和是否已安装 miodesk。**
   Windows、macOS、Linux 各有不同流程；不清楚时提供下方的只读检查命令。
   不要根据浏览器、对话位置猜测本机系统。
2. **再问使用目标**：A. 只给本地 MCP 客户端用；B. 连接当前 ChatGPT；
   C. 先体验。之后确认一个**已经存在的、用户信任的项目文件夹**
   作为 workspace。解释文件工具受工作区限制，但命令工具仍拥有当前
   系统用户的正常权限；不要默认把整个用户主目录或磁盘作为工作区。
3. 检查平台有没有官方预编译包、是否能运行终端／PowerShell。
   如果目标是 ChatGPT，核对其账户／工作区是否有添加自定义 MCP
   的权限和 Tunnel 入口。**不是所有账户都具备相同权限**。
   没有权限时停止远程连接步骤，不要绕过平台限制去暴露公网服务。
4. **只安装 miodesk Core**，优先使用官方 GitHub Release 和 SHA-256
   校验。提供对应系统的复制命令，说明下载执行前应查看安装脚本；
   然后索取 `miodesk version` 输出确认。
5. 配置本地工作区，再运行 `miodesk doctor --json`。
   在 v0.2.2 中稳定支持的无交互方式是
   `miodesk setup --workspace <绝对路径>`；
   较新构建另有 `--non-interactive`，以及只读
   `miodesk preflight --json`。**先查 `miodesk setup -h` 和
   `miodesk help`，有才使用，不能要求旧版执行不存在的命令。**
6. **先用前台方式启动** `miodesk serve`，在第二个终端运行
   `miodesk status`，检查本地 `/healthz`。不要在同一端口启动两个 Core，
   也不要此时安装常驻 systemd／计划任务。
7. **建立分水岭：本地 Core 已通 ≠ ChatGPT 已通。**
   本地 MCP 客户端可选择 stdio（`serve --stdio`）或 loopback HTTP，
   验证一次只读 `status` 即可。需要 ChatGPT 的用户继续下面的 Tunnel 步骤。
8. **ChatGPT 专属阶段**：指导用户在 OpenAI Platform 创建 Tunnel、
   正确关联 Platform 组织和 ChatGPT workspace、检查 Tunnels 权限；
   从官方来源安装 `tunnel-client`；用户在本机创建权限受限的
   runtime key 文件；用 miodesk 建立 profile；确认 tunnel 正在轮询；
   在 ChatGPT Plugins → Add custom MCP server → Tunnel 中接入。
   **必须在 ChatGPT 里真的调用一次 `status`**，才能报告成功。
9. 成功之后再问用户是否要**常驻／开机运行**。
   Linux 可以选择独立的 `miodesk.service` 与
   `miodesk-tunnel.service`，但修改前核查已有 Unit 及凭据来源。
   Windows/macOS 不具备内置 miodesk systemd 管理功能；
   先保持前台运行，之后按用户意愿选择外部进程管理方式。
   实验性 Code Mode 默认关闭，不是安装前置步骤。
10. 完成后向用户说明：此后许多工作区操作、配置文件维护、
    调试和验证可经由真正连接上的 `@miodesk` 工具完成。
    但在连接之前，普通 Chat 模式不能替用户编辑本机。

**安全要求：** 一次问一个主要问题，不要连续输出十几段没有核实的命令。
不要索取密码、API key、token、完整私有配置或密钥截图。
由用户在本机安全地输入和保存凭据；日志／聊天只上传脱敏结果。
不得用 `sudo`、绕过 UAC 或执行策略、关闭防火墙、
开启 `remote.mode=unsafe`、暴露 `0.0.0.0` 来解决连接问题。
不要悄悄覆盖正在使用的 systemd Unit、Tunnel profile 或服务密钥。
用户需要实际执行、批准涉及本机修改的操作。

## 0. 识别系统（安装前，可直接复制）

**Windows PowerShell：**

```powershell
[System.Environment]::OSVersion.VersionString
[System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture
$PSVersionTable.PSVersion.ToString()
```

**macOS / Linux 终端：**

```sh
uname -s
uname -m
```

v0.2.2 正式二进制支持：Linux x64/arm64、macOS x64/arm64、Windows
**x64**。Windows ARM64 尚无官方原生包，不应擅自保证 x64 模拟可用。
其他平台请先检查 [Releases](https://github.com/Chillizu/miodesk/releases)。

## 1. 安装 Core（不需要 OpenAI 账户）

**Linux / macOS：**

```sh
curl -fsSL https://raw.githubusercontent.com/Chillizu/miodesk/main/scripts/install.sh -o miodesk-install.sh
# 先阅读下载的安装脚本
sh miodesk-install.sh
export PATH="$HOME/.local/bin:$PATH"
miodesk version
```

脚本从官方 GitHub Release 下载与系统匹配的可执行文件，
验证 SHA-256 并安装到用户目录。不自动安装官方 Tunnel、不改凭据、
不自动安装服务。如果已有运行中的 miodesk，应先检查是否会覆盖。

**Windows x64 PowerShell：**

```powershell
$installer = Join-Path $env:TEMP "miodesk-install.ps1"
Invoke-WebRequest "https://raw.githubusercontent.com/Chillizu/miodesk/main/scripts/install.ps1" -OutFile $installer
# 先用文本编辑器检查脚本，再执行
& $installer
& "$env:LOCALAPPDATA\Programs\miodesk\miodesk.exe" version
```

Windows 安装器同样验证 SHA-256，但不会自动修改 PATH、启动项、
执行策略、注册表或系统服务。如果 PowerShell 策略阻止执行，
让用户报告**原始错误**，不要直接要求关闭系统保护。

## 2. 配置和检查本地 Core

选择一个**已经存在**的工作区绝对路径。

**Linux / macOS：**

```sh
miodesk setup --workspace /absolute/path/to/project
miodesk doctor --json
miodesk serve
```

**Windows PowerShell：**

```powershell
$miodesk = "$env:LOCALAPPDATA\Programs\miodesk\miodesk.exe"
& $miodesk setup --workspace "C:\path\to\project"
& $miodesk doctor --json
& $miodesk serve
```

先用 `miodesk setup -h` 查看当前发行版是否支持
`--non-interactive`。如果支持，可改用
`miodesk setup --non-interactive --workspace ...`；未支持时，
已有的 `--workspace` 参数本来就不会启动向导。
较新的构建还支持 `miodesk preflight --json`，用于输出
系统架构、发行包、配置状态和 Tunnel 客户端是否安装；
**v0.2.2 不支持此命令**，勿对旧版盲目执行。

保持运行 `serve` 的窗口开启，在**第二个终端**执行：

```sh
miodesk status
```

或 Windows `& $miodesk status`。也可以查看本机
`http://127.0.0.1:8787/healthz`。此时仅证明本地 MCP 可用，
不代表 ChatGPT 已拥有 miodesk 工具。

本地 MCP 客户端也可采用 `serve --stdio` 模式，由客户端自己
启动进程；这种情况下不必另开 HTTP Core。详细见
[README](../README.md)。

## 3. ChatGPT 专属：官方 Secure MCP Tunnel

随时核对 OpenAI 最新官方文档：
[Secure MCP Tunnel](https://developers.openai.com/api/docs/guides/secure-mcp-tunnels)、
[ChatGPT MCP 应用](https://help.openai.com/zh-hans-cn/articles/12584461-creating-custom-mcp-apps-in-chatgpt)。

1. 用户登录 OpenAI Platform 的 Tunnel 设置页，创建／选择 `tunnel_id`，
   确认目标 Platform 组织和 ChatGPT workspace 的关联。
   创建／修改要求 Tunnels Read + Manage；运行客户端或选择
   Tunnel 要求 Tunnels Read + Use。ChatGPT 添加私有 MCP App 的权限
   另行受工作区与账户控制。
2. 只从官方 Platform 入口或官方 `openai/tunnel-client` Release
   安装 `tunnel-client`，检查 `tunnel-client help quickstart`。
3. 用户把 runtime API key **保存在本机私有文件**，绝不发到聊天。
   Unix 文件用 `chmod 600 /absolute/path/to/runtime-key`；
   Windows 要检查实际用户 ACL。不要放在仓库、命令参数、
   截图或公开日志中。
4. 在已有 miodesk 工作区配置上设置官方 profile：

   ```sh
   miodesk setup --tunnel-id tunnel_YOUR_ID --runtime-key-file /absolute/path/to/runtime-key
   miodesk doctor
   tunnel-client doctor --profile miodesk --explain
   ```

   Windows 使用 `& $miodesk setup ...`，其中密钥位置换成真实
   本地文件路径。miodesk 把 profile 格式交给官方客户端处理；
   失败时看脱敏诊断与当前帮助，不手工猜 YAML 格式。
5. Core 前台运行时，在另一终端执行
   `tunnel-client run --profile miodesk`，确认它确实 ready 且
   在轮询 OpenAI。没有现存 Core 的情况下，也可以使用兼容性的
   `miodesk connect` 一次启动两个进程，但**不能与已运行的服务叠加**。
   不需要把本地 8787 端口开放到公网。
6. 在 ChatGPT 的 Plugins 中新增 **private custom MCP server**，
   Connection 选择 **Tunnel**，匹配该 `tunnel_id`，完成权限与风险确认；
   必要时 Refresh 动作列表，新动作可能默认未启用。
   在 ChatGPT 中真正请求一次 `status` 工具并检查响应。

**成功判据：** 不是 `doctor` 全绿、不是 Tunnel 在线、
也不是 App 显示在列表里，而是 **当前 ChatGPT 对话里 miodesk
实际返回了一次工具结果**。

## 4. 连接成功后再决定常驻和增强功能

用户确认希望跨终端、开机保持运行后，Linux systemd 用户可以分别配置：

```sh
miodesk service install
miodesk service start
miodesk tunnel service install
miodesk tunnel service start
```

这是两个**独立的服务**，不是捆绑的同一个服务。
更改前先查看
`systemctl --user cat miodesk.service miodesk-tunnel.service`；
如果存在旧 Unit、`EnvironmentFile`、自定义依赖或正在运行的
前台进程，先确认迁移方案和用户许可。不要覆盖密钥文件；
注销后持续运行可能还需额外用户管理配置，不能默默开启 lingering。

Windows／macOS 可先保持前台工作；如确需常驻，
后续再讨论操作系统自身的启动管理器或计划任务。
miodesk 并未内置对应的后台服务安装功能。

连接后的可选项目还包括：经由 `@miodesk` 更改用户指定的工作区配置、
运行测试和修复环境；Linux 实验性 Code Mode（默认关闭）；
根据实际需要优化性能。**这些都不是首次连接的阻碍。**

## 5. 故障分流和结束条件

如果平台不受支持、校验失败、没有添加自定义 MCP 的权限、
官方 `tunnel-client` 缺失、密钥文件权限错误、
本地 8787 端口被占用、Tunnel 无法连出或 ChatGPT 工具调用失败，
请清楚报告**卡在哪个检查点、实际非敏感错误和下一条安全建议**。
不得无依据地宣称安装完成。

相关资料：[英文原版](AGENT_SETUP.md)、
[完整部署](SETUP.zh-CN.md)、[ChatGPT 指南](CHATGPT.md)、
[安全边界](SECURITY.md)。
