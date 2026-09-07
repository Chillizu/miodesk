# ChatGPT / MCP 配置

`miodesk` 的 Streamable HTTP MCP endpoint 是服务地址后面的 `/mcp`。本地
MCP 客户端可以使用 stdio；ChatGPT 连接器需要一个可访问的 HTTPS 地址。

## 生产部署

首选 [OpenAI Secure MCP Tunnel](https://developers.openai.com/api/docs/guides/secure-mcp-tunnels)。
它让本机的 MCP 服务继续只监听回环地址，由本机 `tunnel-client` 通过出站
HTTPS 连接 OpenAI；不需要把 8787 端口或本机 IP 暴露到公网。官方流程是：

1. 在 OpenAI Platform 的 Tunnel settings 创建 `tunnel_id`，并关联目标
   ChatGPT workspace 与 Platform organization。
2. 安装官方 `tunnel-client`，准备仅用户可读的 runtime API key；不要把 key
   写入 `config.toml`、仓库、systemd unit 或聊天记录。
3. 将 tunnel client 指向本机的 `http://127.0.0.1:8787/mcp`，然后运行其
   `doctor` 和 `run`。
4. 在 ChatGPT Developer Mode 中选择 Tunnel，选择同一个 tunnel。

`tunnel-client` 需要 `tunnel_id`、runtime API key，以及官方文档所述的
workspace/org 权限；本机当前缺少这两个用户凭据，因此 miodesk 不会猜测、
生成或代存它们。

如果不使用 Secure MCP Tunnel，公开 HTTPS 入口必须提供符合 MCP 要求的
OAuth 2.1 认证服务。ChatGPT 文档化的连接器认证是 OAuth 或 no-auth；
miodesk 的静态 bearer token 适合能主动发送 Authorization header 的客户端，
不是 ChatGPT 生产认证方案。

`miodesk connect` 默认生成 bearer token，适合能够发送
`Authorization: Bearer <token>` 的 MCP 客户端；ChatGPT 当前文档化的连接器
认证选项是 `noauth` 或 OAuth，因此静态 token 不是 ChatGPT 的生产认证方案。

## 本机 MCP 客户端

stdio 配置示例：

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

这段配置适用于本地 MCP host，不是 ChatGPT 的远程 connector 配置。

## 免费临时测试

本机已验证 Tailscale Funnel 可用。启动一个短期测试入口：

```sh
miodesk connect --provider tailscale --unsafe-remote --port 0
```

命令会打印类似下面的地址：

```text
https://<your-tailnet-host>/mcp
```

在 ChatGPT Developer Mode/connector 表单中填写：

```text
Endpoint URL:    https://<your-tailnet-host>/mcp
Authentication:  No authentication
```

这是明确的 `unsafe` 模式：拿到 URL 的任何人都可以读写 workspace，并执行
当前用户权限内的命令。仅用于短时、隔离 workspace 的联调；测试结束后按
`Ctrl-C` 停止 `miodesk`，确认 Funnel 已关闭。不要把这个模式当作生产部署。

ChatGPT 的 Developer Mode、远程 MCP 和 connector 可用性还受账号/工作区
权限控制，具体入口以[官方说明](https://help.openai.com/en/articles/12584461-developer-mode-and-full-mcp-connectors-in-chatgpt)
为准。

## 测试时查看日志

当前 systemd 服务把 miodesk 日志收进 user journal。推荐先开一个终端：

```sh
miodesk logs --follow --json
```

重点事件包括 `http_request`、`auth_denied`、`mcp_tool_complete`、
`mcp_tool_error`、`http_panic`；每条记录都有 `request_id`，可与 HTTP 响应的
`X-Miodesk-Request-ID` 对照。日志默认不含文件内容、命令正文、请求
Authorization 或任何 token。
