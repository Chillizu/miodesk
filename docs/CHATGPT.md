# ChatGPT 连接与插件

**简体中文** · [English](CHATGPT.en.md) · [Agent 引导安装](AGENT_SETUP.zh-CN.md)

miodesk 是独立的本地 MCP Core。它默认监听
`http://127.0.0.1:8787/mcp`，不要求使用 ChatGPT 或 OpenAI Tunnel。

**ChatGPT 无法直接访问另一台电脑的 `127.0.0.1`。** 对于支持该能力的
ChatGPT／OpenAI Platform 环境，推荐通过官方
[OpenAI Secure MCP Tunnel](https://developers.openai.com/api/docs/guides/secure-mcp-tunnels)
建立出站连接；也可以由操作者自行提供安全的 HTTPS MCP 入口。

## 1. 连接 ChatGPT

先参照 [设备与 Tunnel 设置指南](SETUP.md)完成以下前置条件：

- 本地 `miodesk setup` 与 `miodesk doctor` 正常；
- 在 OpenAI Platform 创建 tunnel，并关联目标组织及 ChatGPT workspace；
- 安装官方 `tunnel-client`，以权限受限的文件保存 runtime API key；
- 为 miodesk 设置 tunnel ID、key 文件路径，并验证 tunnel-client profile。

配置示例（请替换为真实 ID 和绝对路径）：

```sh
miodesk setup \
  --workspace /absolute/path/to/workspace \
  --tunnel-id tunnel_YOUR_ID \
  --runtime-key-file /absolute/path/to/openai-runtime-key
miodesk doctor
tunnel-client doctor --profile miodesk --explain
```

用于一次性前台连接的兼容命令是 `miodesk connect`：它会同时启动
Core 和 Tunnel，按 Ctrl-C 停止。**不要在已有 Core 或 tunnel-client
运行时再启动一份同端口／同 profile 的连接。**

Linux 持久运行时更适合独立服务：

```sh
miodesk service install
miodesk service start
miodesk tunnel service install
miodesk tunnel service start
```

`miodesk.service` 和 `miodesk-tunnel.service` 各自管理生命周期；
升级／重启 Core 不要求重启 Tunnel。已有自定义 systemd unit 可能
仍带有 `PartOf`、`Wants`、`Requires` 或 `EnvironmentFile`，
修改前请按 [安全迁移步骤](SETUP.md#4-start-the-connection) 审查，
不要覆盖密钥来源。

在 ChatGPT 的 Developer Mode／连接器设置中选择对应 Tunnel。
不要将本机 loopback URL 填进远程连接表单，也无需为此开启 miodesk
的 `unsafe` 或静态 bearer-token 模式。实际入口及权限以
[ChatGPT 官方说明](https://help.openai.com/en/articles/12584461-developer-mode-and-full-mcp-connectors-in-chatgpt)
为准。

## 2. 工具更新与 Code Mode

标准 MCP `tools/list` 包含 **10 个原生工具**。Linux 上显式配置
`[codemode] enabled = true`，并重启 Core 后，才会额外出现第十一个
实验性、只读的 `code` 工具。参见
[Code Mode (Experimental)](CODEMODE.md)。

**Tunnel 只转发 MCP 请求，不刷新 ChatGPT 已注册 App 的工具快照。**
服务端 schema 变化后，需要在 ChatGPT 的 action 控制／管理界面
显式 **Refresh**；新增动作可能需要另行启用。不要因为客户端仍
显示 `command`／`command_start`／`command_poll`／
`command_cancel` 等旧工具，就把它们重新加入服务端 `tools/list`：
当前 Core 只广告 `exec_command`／`write_stdin`，
历史兼容层只是临时处理旧缓存的调用。

## 3. 可选的私有 ChatGPT 插件包装

`plugins/miodesk/` 是独立于 Core 与 Tunnel 的**私有插件包装材料**。
它使用 Agent Plugins 根级 `plugin.json`，通过
`extensions.com.openai.apps` 关联你在 ChatGPT 实际注册的 MCP App。
该仓库只跟踪 `.app.example.json`，**不包含任何实际 App ID 或密钥**。

包装前应：

1. 复制 `plugins/miodesk/.app.example.json` 为
   `plugins/miodesk/.app.json`（后者已被 Git 忽略）。
2. 填入你自己已注册的 MCP App ID，并在当前 ChatGPT 环境核对。
3. 按插件管理界面／Plugin Creator 的当前要求打包、安装，随后
   在新对话中验证 `status` 和代表性的工具操作。

`plugins/miodesk/.codex-plugin/plugin.json` 保留为旧客户端兼容清单，
logo 与技能说明仍独立打包。**GitHub Release 不会自动发布或更新
ChatGPT 插件**，也不会替用户打开实验性 `code` 工具。

公共 Plugins Directory 提交要求另行验证；开发机使用的
OpenAI Secure MCP Tunnel 不能直接等同于一个公共 HTTPS MCP endpoint。

## 4. 故障排查

```sh
miodesk status
miodesk doctor
miodesk tunnel doctor
tunnel-client doctor --profile miodesk --explain
```

- ChatGPT 找不到新动作：先检查 Core 的工具列表和连接器 Refresh。
- Tunnel 不在线：检查 `tunnel-client` profile、key 文件权限及
  外部控制平面状态；不要为此开放本机端口或关闭认证。
- 旧 systemd unit：先 `systemctl --user cat miodesk-tunnel.service`，
  再参照 [SETUP.md](SETUP.md) 迁移。
- 日志：前台运行看终端；Linux 用户服务运行时可用
  `miodesk logs --follow --json`。日志只包含诊断元数据与请求 ID，
  不应包含文件正文、命令文本、Authorization 或密钥。
- 本机健康检查：`curl --fail http://127.0.0.1:8787/healthz`。

更完整的 HTTP 安全模式、自定义 HTTPS 入口和端口调整，
请参阅 [SECURITY.md](SECURITY.md) 与 [SETUP.md](SETUP.md)。
