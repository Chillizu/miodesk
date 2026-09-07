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

## 启动方式

前台一条命令会同时启动本地 MCP server 和 tunnel：

```sh
miodesk connect
```

按 `Ctrl-C` 会停止两者。若 Linux 上已经安装并启动了 miodesk user service，
`miodesk connect` 会复用正在运行的本地 server，只在当前终端运行 tunnel：

```sh
miodesk service install
miodesk service start
miodesk connect
```

`service install` 不会隐式 enable；确认运行正常后再执行：

```sh
systemctl --user enable miodesk
```

macOS/Windows 没有内置的 miodesk service 管理器，使用前台命令或操作系统
自己的进程管理工具即可。

## 自定义端口

端口是本机 server 与 tunnel-client 之间的目标，不是公网端口。需要换端口
时使用固定值，并重新运行 setup 让 profile 同步：

```sh
miodesk setup --workspace /absolute/path/to/workspace --port 9900
```

然后再运行 `miodesk connect`。`--port 0` 只适用于本地临时测试；OpenAI
Tunnel 需要固定端口。

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

`Error loading app / Failed to fetch template` 属于 ChatGPT/MCP Apps 获取
widget resource 失败，不等同于 MCP tool 逻辑失败。常规 read/search/list/
write/edit/delete/short command 使用 Native-first 返回；只有 status 和长任务
生命周期结果声明可选 widget。此时先确认 endpoint、Tunnel 选择和
`miodesk logs`，不要把 widget 错误误判成文件工具不可用。
