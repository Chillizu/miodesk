<p align="center">
  <img src="plugins/miodesk/assets/miodesk-logo.png" width="150" alt="miodesk logo">
</p>

<h1 align="center">miodesk</h1>
<p align="center"><strong>让 ChatGPT 和开发 Agent 安全地接触你自己的工作区。</strong></p>
<p align="center"><a href="README.md">English</a> · <strong>简体中文</strong></p>

miodesk 是用 **Go** 编写的轻量级本地 MCP Server。它提供文件读写、搜索、
命令执行、会话续接等能力，可接入本地 MCP 客户端；也可通过**独立、可选的**
OpenAI Secure MCP Tunnel 连接到 ChatGPT。

**核心特性：** 单二进制运行、默认仅监听本机、10 个原生 MCP 工具、
有边界的文件操作与命令会话、分离的 Core / Tunnel 生命周期。
Linux 上另有默认关闭的**实验性 Code Mode**（只读 JavaScript 工具组合）。

> **安全提醒：** 文件工具会校验工作区边界；但 `exec_command` 等
> Shell 工具仍以你的系统用户权限运行，**不是完整的 OS 沙箱**。
> 只授权你信任的客户端和项目工作区，详见 [安全模型](docs/SECURITY.md)。

## 用 ChatGPT 一句话引导安装

还没连接 miodesk，也可以在**普通 ChatGPT 聊天**中直接粘贴这段提示词。
ChatGPT 阅读公开的 Agent 指南后，引导你在**自己的电脑**上逐步安装：

```text
阅读 https://raw.githubusercontent.com/Chillizu/miodesk/main/docs/AGENT_SETUP.zh-CN.md ，一步步指导我安装 miodesk 并连接到 ChatGPT，每次给我一整块可以复制执行的命令。
```

[中文 Agent 安装手册](docs/AGENT_SETUP.zh-CN.md) · [遇到问题？](docs/FEEDBACK.md) · [English prompt](README.md#one-prompt-setup-with-chatgpt-no-connector-needed-yet)

**安装的三个检查点：** Core 本地可用 → 官方 Tunnel 连接成功 →
**ChatGPT 中实际调用 miodesk 工具成功**。前两个检查点不能代替第三个。
如果你的 ChatGPT 账户／工作区没有使用自定义 MCP 的权限，
应如实停止远程配置，不建议绕过限制开放公网。

## 快速开始：本地 MCP

### 1. 安装

**Linux / macOS：**

```sh
curl -fsSL https://raw.githubusercontent.com/Chillizu/miodesk/main/scripts/install.sh -o miodesk-install.sh
# 先查看脚本内容，再执行
sh miodesk-install.sh
export PATH="$HOME/.local/bin:$PATH"
miodesk version
```

安装脚本从正式 [GitHub Releases](https://github.com/Chillizu/miodesk/releases)
下载安装包并验证 SHA-256，不自动创建服务、配置密钥或启动 Tunnel。

**Windows x64 PowerShell：**

```powershell
$installer = Join-Path $env:TEMP "miodesk-install.ps1"
Invoke-WebRequest "https://raw.githubusercontent.com/Chillizu/miodesk/main/scripts/install.ps1" -OutFile $installer
# 先在编辑器中查看脚本，再运行
& $installer
& "$env:LOCALAPPDATA\Programs\miodesk\miodesk.exe" version
```

PowerShell 安装器同样验证发行包校验和，不自动修改用户 PATH、
执行策略、系统服务或防火墙。Windows ARM64 尚无官方原生发布包；
请核实 [平台资产](https://github.com/Chillizu/miodesk/releases)。
若执行策略阻止脚本，请先了解当前策略，不要盲目关闭保护。

### 2. 配置工作区

**Linux / macOS：**

```sh
mkdir -p "$HOME/miodesk-demo"
cd "$HOME/miodesk-demo"
miodesk setup --workspace "$PWD"
miodesk doctor --json
```

**Windows PowerShell：**

```powershell
$miodesk = "$env:LOCALAPPDATA\Programs\miodesk\miodesk.exe"
$workspace = Join-Path $env:USERPROFILE "miodesk-demo"
New-Item -ItemType Directory -Force -Path $workspace | Out-Null
& $miodesk setup --workspace $workspace
& $miodesk doctor --json
```

首次设置默认仅本地 MCP（`127.0.0.1:8787`），不会自动连接 ChatGPT。
如果不带参数且有真实终端，`miodesk setup` 可进入交互向导。
**Agent 应优先传入明确的工作区目录**，不要默认开放整个主目录。

从 **v0.2.3** 起提供只读 `miodesk preflight --json` 和
`miodesk setup --non-interactive --workspace ...`；旧版的
`setup --workspace` 也能避免弹出向导。

### 3. 启动并检查

```sh
miodesk serve
```

Windows 则是 `& $miodesk serve`。保持这一个前台窗口运行，
在第二个终端执行 `miodesk status`（Windows：
`& $miodesk status`）。

MCP 本地地址为 `http://127.0.0.1:8787/mcp`，
健康检查为 `http://127.0.0.1:8787/healthz`。

### 接入本地 MCP 客户端

支持 stdio 的 MCP 客户端可自行启动 miodesk，通常无需额外启动
HTTP Core：

```json
{
  "mcpServers": {
    "miodesk": {
      "command": "/absolute/path/to/miodesk",
      "args": ["serve", "--stdio"]
    }
  }
}
```

将路径换成实际的可执行文件绝对路径，不要把示例地址直接复制过去。

## 连接到 ChatGPT（可选）

ChatGPT 无法直接访问另一台设备的 `127.0.0.1`。
请参阅 [Agent 安装指南](docs/AGENT_SETUP.zh-CN.md)、
[设备与 Tunnel 指南](docs/SETUP.zh-CN.md) 和
[ChatGPT 专门说明](docs/CHATGPT.md)。

需要先获得 OpenAI Platform 的 `tunnel_id`、
官方 `tunnel-client` 和保存在**本机私有文件**中的 runtime key。
在权限和关联关系正确的情况下：

```sh
miodesk setup \
  --tunnel-id tunnel_YOUR_ID \
  --runtime-key-file /absolute/path/to/runtime-key
miodesk doctor
tunnel-client doctor --profile miodesk --explain
```

运行中的 Core 与官方 `tunnel-client run --profile miodesk`
位于不同终端／进程。然后在 ChatGPT Plugins 中添加 **private custom
MCP server**，连接方式选 Tunnel，检查必要权限和风险确认，
**在 ChatGPT 里调用一次真实的 `status`**。

若机器尚未启动其他 Core，可使用兼容性的 `miodesk connect`
前台同时启动两个进程；避免与已经在运行的服务重复。

## 工具列表

| 工具 | 用途 |
| --- | --- |
| `read` / `search` / `list` | 按行读取、文字精确搜索、目录浏览 |
| `write` / `edit` / `delete` | 限定工作区中的文件修改 |
| `exec_command` / `write_stdin` | 有界命令执行、长命令轮询、输入／取消 |
| [`context`](docs/CONTEXT.zh-CN.md) | 持久化任务交接与跨会话续接；查看字段、检查点与恢复机制的完整说明 |
| `status` | 查看连接状态和简洁诊断 |

## 连接成功之后再考虑常驻

对 Linux/systemd 用户，miodesk Core 与官方 OpenAI Tunnel 有
**两个独立的服务**：

```sh
miodesk service install
miodesk service start
miodesk tunnel service install
miodesk tunnel service start
```

只有在用户选择常驻、排除已存在进程，并核对旧 systemd Unit 的
凭据引用后才执行；**不要在首次安装时无提示启用服务**。
Windows/macOS 没有内置的 miodesk systemd 管理器，可以先使用前台窗口。

Linux 还有默认关闭的 [实验性 Code Mode](docs/CODEMODE.zh-CN.md)，
可通过 `[codemode] enabled = true` 多暴露一个只读 `code` 工具。
它有进程资源限制，**但不是完整 OS 安全沙箱**，不是连接 ChatGPT
的必要条件。

## 配置、开发及故障排查

配置路径：`~/.config/miodesk/config.toml`（Windows 和
非默认 XDG 设置请以 `miodesk config` 返回路径为准）。

```toml
[server]
host = "127.0.0.1"
port = 8787

[workspace]
root = "/absolute/path/to/a/trusted/project"

[tunnel]
provider = "local"
```

- `miodesk: command not found`：检查 PATH 或使用安装器打印的绝对路径。
- 8787 被占用：用 `miodesk status` 判断已有服务，不要重复启动。
- 文件访问失败：查看 `miodesk workspace` 和文件权限。
- ChatGPT 中仍显示旧工具：刷新已注册 MCP App 的 action 列表。
- Tunnel 失败：运行 `miodesk tunnel doctor` 并查看官方客户端诊断。
- 查看日志：Linux service 可用 `miodesk logs --follow`。

源代码测试：

```sh
go test -vet=all ./...
scripts/release-check.sh
bash scripts/onboarding-test.sh
```

## 反馈与问题报告

安装卡住或连接失败？可直接[提交 Bug 反馈](https://github.com/Chillizu/miodesk/issues/new?template=bug_report.yml)或[提出体验建议](https://github.com/Chillizu/miodesk/issues/new?template=feature_request.yml)。说明系统、版本和出错阶段即可，**不要发布密钥、Tunnel ID、私人路径或原始日志**。安全漏洞请走[私密报告渠道](https://github.com/Chillizu/miodesk/security/advisories/new)。更多见[中英双语反馈指南](docs/FEEDBACK.md)。

[CHANGELOG](CHANGELOG.md) · [安全模型](docs/SECURITY.zh-CN.md) ·
[发行说明](https://github.com/Chillizu/miodesk/releases) ·
[英文 README](README.md) · [MPL-2.0](LICENSE)
