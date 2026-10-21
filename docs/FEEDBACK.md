# Feedback & support / 反馈与支持

If something goes wrong while installing or using miodesk, you do **not**
need to understand MCP internals. Report the first failing checkpoint, the
operating system, the miodesk version and what happened.

使用 miodesk 遇到问题时，不需要先研究 MCP 内部原理。只要说明：
**卡在哪一步、系统及架构、miodesk 版本、实际发生了什么**。

- **[Report a bug / 报告问题](https://github.com/Chillizu/miodesk/issues/new?template=bug_report.yml)**
- **[Suggest an improvement / 提出建议](https://github.com/Chillizu/miodesk/issues/new?template=feature_request.yml)**
- **[Privately report a vulnerability / 私密报告安全漏洞](https://github.com/Chillizu/miodesk/security/advisories/new)**

## Quick diagnostic / 快速诊断

If installed, run **one block for your system**, inspect the output before
sharing, and remove anything sensitive. The commands read state only, and no
feedback or diagnostic information is uploaded automatically.

已经安装 miodesk 的话，可运行下面对应系统的只读命令；**先审查输出并
脱敏，再决定贴到 GitHub**。

**Linux / macOS:**

```sh
miodesk version
miodesk preflight --json
miodesk doctor --json
```

**Windows PowerShell (default installer path):**

```powershell
$miodesk = Join-Path $env:LOCALAPPDATA 'Programs\miodesk\miodesk.exe'
& $miodesk version
& $miodesk preflight --json
& $miodesk doctor --json
```

`preflight` is available from **v0.2.3**; for earlier versions, omit that
line. `doctor --json` may contain a **workspace path and other environment
details**. Check every line. Do **not** post secrets, runtime API keys,
bearer tokens, Tunnel IDs, full `config.toml`, raw terminal history, or
unredacted logs. A sanitized minimal error is more useful than a huge dump.

`preflight` 自 **v0.2.3** 起提供，旧版跳过这一行。`doctor --json`
可能含**工作区路径等环境信息**，不能直接无脑上传。不要公开
runtime key、token、Tunnel ID、完整 `config.toml`、历史命令或
未脱敏日志。提供最小复现和几行脱敏诊断即可。

## Give feedback during ChatGPT-assisted setup / 安装中反馈

Tell the guiding ChatGPT assistant which checkpoint failed and paste only
redacted output. Ask it to explain the error and help you open an issue if it
cannot fix it with a safe, verified action. Do not ask the assistant to
automatically publish private diagnostics.

向正在引导安装的 ChatGPT 说明**哪个检查点失败**，只给它脱敏结果。
如果无法排除，可让它协助整理 Issue 文案；未经审查不要自动公开日志或
私有配置。安全漏洞优先走上面的**私密报告**入口。

The [English](AGENT_SETUP.md) and [Chinese](AGENT_SETUP.zh-CN.md)
setup guides are maintained together. The **Experimental Linux Code Mode**
is optional and not required to connect.
