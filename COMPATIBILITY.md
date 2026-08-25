# Minecraft Java Edition 兼容性

本文档是本分支的兼容性约定，特意将版本识别、线路协议兼容性和游戏玩法支持区分开来。

最近一次目录基线：2026-08-26；当时最新的 Java Edition 稳定版本为 26.2。

## 范围

包含：

- Java Edition 从 1.0 到 26.2 的所有稳定版产物；
- Netty 之前的 1.0-1.6 传输协议系列；
- 1.7 引入的长度前缀 Netty 传输协议；以及
- 从 26.1 开始采用年份命名的版本。

不包含：

- 快照版、预发布版、候选发布版和愚人节版本；
- Classic、Indev、Infdev、Alpha 和 Beta；以及
- Bedrock Edition。

当前目录共包含 103 个命名版本、24 条主版本发布线、63 个不同的协议整数，以及 64 个无歧义的线路协议档案。多出的一个档案是有意保留的：协议整数 47 曾被互不兼容的旧式 1.4.2 传输协议和 Netty 1.8 传输协议重复使用。

## 支持等级

| 等级 | 含义 |
| --- | --- |
| `Unsupported` | 实现不存在、复用不安全，或尚未得到证明。API 必须明确拒绝该操作。 |
| `Experimental` | 线路格式已有确定性测试，但原版互操作和边界情况仍未完整验证。 |
| `Verified` | 确定性测试和对应版本原版实现的互操作证据均已在 CI 中通过。 |

某个版本出现在 `protocol.Versions()` 中，仅表示其标识已知，并不会自动提升任何子系统的能力等级。

## 当前矩阵

| 协议系列 | 版本范围 | Status | 独立 Login | Configuration | Play 核心 | Chat / Inventory / World | 完整 Server |
| --- | --- | --- | --- | --- | --- | --- | --- |
| 旧式 Ping | 1.0-1.6.4 | Experimental | Unsupported | N/A | Unsupported | Unsupported | Unsupported |
| 早期 Netty | 1.7.2-1.18.2 | Experimental | Experimental | N/A | Experimental | Unsupported | Unsupported |
| 签名档案过渡期 | 1.19-1.20.1 | Experimental | Experimental | N/A | Experimental | Unsupported | Unsupported |
| Configuration 过渡期 | 1.20.2-1.20.6 | Experimental | Experimental | Experimental | Experimental | Unsupported | Unsupported |
| 当前实现基线 | 1.21 / 1.21.1 (p767) | Experimental | Experimental | Experimental | Experimental | Unsupported | Unsupported |
| 后续稳定版档案 | 1.21.2-26.2 | Experimental | Experimental | Experimental | Experimental | Unsupported | Unsupported |

“Play 核心”表示 `JoinServer` 会完成 Handshake、Login 以及存在时的 Configuration，并通过经过审计的语义 ID 映射公开已分帧的 Play 数据包。它并不表示所有 Play 载荷都能被解码。协议 767 仍是旧式数字事件处理器的深度编解码基线；可移植的原始数据包处理器应使用 `Events.AddSemanticListener`。对于 p767 之外的 Configuration，实现会有意将不阻塞状态切换的注册表/世界载荷保留为不透明数据，同时严格应答所有会阻塞状态切换的请求。

权威的运行时取值始终是：

```go
profile.Capabilities()
```

此表仅用于说明，不得取代生成的档案数据。

## 验证门槛

只有通过所有适用门槛后，才能提升一项能力：

1. 数据包模式来自固定且可审查的来源；
2. 编码/解码测试覆盖该系列的每一个协议边界；
3. 畸形或未知数据会返回错误，而不是引发 panic 或悄然造成数据流失步；
4. 客户端和服务端测试均验证两个方向；并且
5. 提升为 `Verified` 还必须具有对应版本的原版客户端/服务端夹具，或在 CI 中完成互操作运行。

CI 会在 Linux、macOS 和 Windows 上，使用 Go 1.22 和稳定版 Go 执行格式检查、模块整洁度检查、vet、单元测试和集成测试；此外还包含竞态检测、覆盖率、CodeQL、依赖更新和已知漏洞扫描。

### 受门控的原版互操作通道

`.github/workflows/vanilla-integration.yml` 提供每月、手动运行以及显式 `vanilla-test-*` 测试标签触发的官方原版服务端互操作证据。测试标签入口用于在功能分支尚未合并到默认分支时验证精确提交，并且仍受下述仓库级 EULA 门控。旧式夹具（1.2.5 至 1.6.4）仅测试 Status。每个 Netty 夹具会先通过精确版本的 Status 就绪检查，再使用离线模式客户端完成 Handshake、Login、存在时的 Configuration，并进入 Play。验证器会在同一个严格的尝试截止时间内读取首个 Play 数据包，并要求其档案特定的语义种类为 `login`（Join Game）。夹具会明确设置 `enable-code-of-conduct=false`；与此同时，验证器不会安装同意回调，因此若服务端仍发送 Code of Conduct 数据包，验证会失败，而不会悄然接受。

除非有人在手动输入中勾选 `accept_eula`，或仓库管理员设置 `MINECRAFT_EULA_ACCEPTED=true`，否则该通道会保持禁用。没有这一明确门控，工作流绝不会写入 `eula=true` 或启动服务端；本项目不会代替贡献者接受 Mojang 的 EULA。测试服务器同时显式设置 `online-mode=false` 与 `enforce-secure-profile=false`，因此该通道验证离线模式的线路互操作，不把 Mojang 会话服务可用性混入协议矩阵。

矩阵会为目录中每个主版本选取当前 Mojang 官方启动器元数据仍提供服务端下载的最新稳定补丁版，并额外包含 1.7.2，从而同时为协议 4 和协议 5 提供原版 Netty 覆盖：

| Java | 精确的原版夹具 |
| --- | --- |
| 8 | 1.2.5, 1.3.2, 1.4.7, 1.5.2, 1.6.4, 1.7.2, 1.7.10, 1.8.9, 1.9.4, 1.10.2, 1.11.2, 1.12.2, 1.13.2, 1.14.4, 1.15.2, 1.16.5 |
| 16 | 1.17.1 |
| 17 | 1.18.2, 1.19.4 |
| 21 | 1.20.6, 1.21.11 |
| 25 | 26.1.2, 26.2 |

执行前会校验清单元数据的 SHA-1，以及各版本元数据中服务端文件的 SHA-1 和大小；Java 版本选择也来自相应元数据。1.6.4 是已记录的例外：其当前元数据省略了 `javaVersion`，因此该通道明确使用 Java 8。当前 1.0 和 1.1 的元数据没有 `downloads.server` 产物，因此这些目录版本无法由此官方服务端通道覆盖；项目不会使用非官方镜像。

验证器会选择精确的版本别名及其 `protocol.ByName` 档案，在有界截止时间内重试完整的 Status 后 Login 尝试，并要求规范化 Status JSON 中的协议号和版本名称相符。原版 1.2/1.3 的 `0xFE` 响应不会传输这两个标识字段，因此这两次运行只能证明传输协议和响应解析有效；规范化后的标识只能来自所选目录档案。这一限制不会被视为独立的、由服务端报告的标识证据。仅有旧式 Status 结果不会提升 Login、Configuration、Play 或完整 Server 的支持等级；Netty 进入 Play 的结果只覆盖状态切换和首个 Join Game 数据包，不覆盖后续游戏玩法子系统。

## 数据与更新

离线生成器、固定输入、校验和、对账规则和更新命令均记录在 [`protocol/SOURCES.md`](protocol/SOURCES.md) 中。新版本通过经过审查的来源更新进入目录。在某个子系统的测试足以证明其能力之前，新版本在该子系统中仍保持不支持状态。
