# ChatGPT / MCP 连接

`miodesk` 的 Streamable HTTP MCP endpoint 是本机服务地址后面的 `/mcp`。
本地 MCP host 可以使用 stdio；ChatGPT 不应直接访问另一台设备的
`127.0.0.1`，应使用 OpenAI Secure MCP Tunnel 或用户自己管理的 HTTPS 入口。

## 推荐：OpenAI Secure MCP Tunnel

这是默认连接方式。服务器继续只监听 `127.0.0.1`，本机官方
`tunnel-client` 通过出站 HTTPS 连接 OpenAI，再把 MCP 请求转发到本地
`/mcp`。因此不需要开放 8787、暴露本机 IP，也不需要在 ChatGPT 表单中粘贴
一个本地 URL。

前置条件：

1. 在 [OpenAI Secure MCP Tunnel 文档](https://developers.openai.com/api/docs/guides/secure-mcp-tunnels)
   所述的平台位置创建 tunnel，并将它关联到目标 ChatGPT workspace 和
   Platform organization。
2. 安装官方 `tunnel-client`。
3. 准备 runtime API key 文件。不要把 key 写入仓库、`config.toml`、
   systemd unit 或聊天记录；Unix 下应使用 `chmod 600`。

然后在新设备的工作区目录运行：

```sh
miodesk setup \
  --workspace /absolute/path/to/workspace \
  --tunnel-id tunnel_… \
  --runtime-key-file /absolute/path/to/openai-runtime-key
miodesk doctor
tunnel-client doctor --profile miodesk --explain
miodesk connect
```

`miodesk setup` 会让 `tunnel-client` 生成或刷新 `miodesk` profile，并将本地
target 写成固定端口（默认 `http://127.0.0.1:8787/mcp`）。它只保存 tunnel
ID 和文件路径，不读取、打印或复制 key 内容。profile 的 YAML 格式由
`tunnel-client` 负责，miodesk 不自行重写它。

在 ChatGPT Developer Mode/connector 设置中选择对应的 Tunnel。使用这个
流程时，不要选择 miodesk 的静态 bearer token，也不需要把认证改成 OAuth；
Tunnel 本身就是 OpenAI 连接边界。ChatGPT 的可用入口仍受账号和 workspace
权限控制，具体界面以[官方说明](https://help.openai.com/en/articles/12584461-developer-mode-and-full-mcp-connectors-in-chatgpt)
为准。

## Plugin Creator 包装

`plugins/miodesk/` 使用 Agent Plugins 1.0 根级 `plugin.json`，并在
`extensions.com.openai` 中映射 `.app.json` 里的已注册 MCP app。`.app.json` 是每位使用者私有的
MCP app 绑定，不提交到仓库；先复制 `.app.example.json` 为 `.app.json`，再填入当前
ChatGPT 中实际注册的 app ID，核对无误后才打包安装。`.codex-plugin/plugin.json`
保留为旧客户端兼容清单；`assets/miodesk-logo.png` 同时用于插件 logo 和 composer 图标。
技能目录只保留工作区检查、授权修改、命令边界和会话续接等必要约束。该插件继续复用
Developer Mode 中注册的 MCP app 和 Secure MCP Tunnel，不替代 miodesk server、Tunnel
或 MCP app 注册。

使用流程：

1. 在 ChatGPT Plugins 中保留已注册的 miodesk MCP app，并在服务 schema 变更后刷新它。
   将 `plugins/miodesk/.app.example.json` 复制到 `plugins/miodesk/.app.json`，
   用自己当前注册的 app ID 替换示例值（此文件会被 Git 忽略）。
2. 在 Plugins Directory 中打开并安装私有 `miodesk` 插件。
3. 在新对话中启用插件并核对工具列表，再做只读状态调用和代表性文件操作。

截至 2026-09-23，用户已在新对话中手动确认刷新后出现新 schema。ChatGPT 将
“创建 MCP 应用”入口移到二级菜单这一点本身不能证明该功能即将弃用；当前
Plugin Creator 文档仍要求先注册 MCP app，再通过 app ID 创建插件包装。
如果将来要发布到公共 Plugins Directory，需要另行评估公开 HTTPS MCP endpoint；
OpenAI Secure MCP Tunnel 用于本地开发连接，不满足公共插件提交的 endpoint 要求。

## 启动方式

前台一条命令会同时启动本地 MCP server 和 tunnel：

```sh
miodesk connect
```

按 `Ctrl-C` 会停止两者。Linux 上可以把本地 server 和 tunnel-client 都交给
systemd user service：

```sh
miodesk service install
miodesk service start
```

OpenAI Tunnel 已配置时，安装会生成 `miodesk.service` 和
`miodesk-tunnel.service`；后者依赖本地 server，并在 tunnel-client 异常退出时
自动重启。若两者已经在后台运行，`miodesk connect` 只报告现有连接，不会再
启动第二个 tunnel-client。

`service install` 不会隐式 enable；确认运行正常后再执行：

```sh
systemctl --user enable miodesk miodesk-tunnel
```

macOS/Windows 没有内置的 miodesk service 管理器，使用前台命令或操作系统
自己的进程管理工具即可。

## 自定义端口

端口是本机 server 与 tunnel-client 之间的目标，不是公网端口。需要换端口
时使用固定值，并重新运行 setup 让 profile 同步：

```sh
miodesk setup --workspace /absolute/path/to/workspace --port 9900
```

若使用 systemd 服务，再运行 `miodesk service restart`；前台模式则重新运行
`miodesk connect`。`--port 0` 只适用于本地临时测试；OpenAI Tunnel 需要固定端口。

## 已有 HTTPS 入口（高级）

如果操作者已经拥有反向代理或其他 HTTPS ingress，可使用：

```sh
miodesk connect --provider custom --url https://mcp.example.com
```

这不会创建、验证或保护公网入口；操作者必须自行把 `/mcp` 路由到本地
server，并配置 connector 支持的认证。ChatGPT 文档化的远程 connector
认证是 OAuth 2.1 或明确的 no-auth 测试；miodesk 的静态 bearer token 不是
ChatGPT 的文档化 connector 认证选项。这个模式不属于默认 onboarding。

## 本地 MCP host

stdio 配置不需要网络：

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

## 故障排查和日志

先执行：

```sh
miodesk doctor
miodesk tunnel doctor
tunnel-client doctor --profile miodesk --explain
```

Linux user service 的日志：

```sh
miodesk logs --follow --json
```

重点事件包括 `http_request`、`auth_denied`、`mcp_tool_complete`、
`mcp_tool_error`、`http_panic`；每条记录都有 `request_id`，可与 HTTP 响应的
`X-Miodesk-Request-ID` 对照。日志默认不含文件内容、命令正文、请求
Authorization 或任何 token。没有安装 service 时，前台运行的日志直接出现在
终端。

本地健康检查：

```sh
curl --fail http://127.0.0.1:8787/healthz
```

如果 ChatGPT 仍显示旧的 `command` / `command_start` / `command_poll` /
`command_cancel` schema，而本地 `tools/list` 已经显示 `exec_command` /
`write_stdin`，这是客户端/connector 的 schema 缓存，而不是 server 回退。当前
server 只 advertise 新工具；一个窄的临时兼容层会接住旧调用，但不会把旧工具
重新放回 `tools/list`，也不会向旧调用注入新 schema 才有的可选字段。优先重载
connector/client 来刷新 schema，不要为了迎合缓存再次扩张旧工具面。

`status` 是只读 MCP 工具，返回简洁的文本/结构化诊断；miodesk 不发布
MCP Apps UI 模板或 widget 资源。若旧会话仍显示模板加载错误，应刷新
connector/client 的工具 schema，并通过 `status` 和 `miodesk logs` 核查实际服务。
