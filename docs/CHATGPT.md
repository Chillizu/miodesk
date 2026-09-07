# ChatGPT / MCP 配置

`miodesk` 的 Streamable HTTP MCP endpoint 是服务地址后面的 `/mcp`。本地
MCP 客户端可以使用 stdio；ChatGPT 连接器需要一个可访问的 HTTPS 地址。

## 生产部署

优先选择以下方案之一：

1. 使用 [OpenAI Secure MCP Tunnel](https://developers.openai.com/api/docs/guides/secure-mcp-tunnels)，把本机的
   `miodesk /mcp` 通过出站隧道交给 ChatGPT；这需要在 OpenAI 侧创建
   `tunnel_id`、运行时 API key，并完成对应 workspace/org 关联。
2. 在公开 HTTPS 入口前提供符合 MCP 要求的 OAuth 2.1 认证服务。

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
