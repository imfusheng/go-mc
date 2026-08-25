# 协议目录来源

该目录列出了 Minecraft Java Edition 的版本元数据。存在某个条目只表示该版本及其线路配置档身份已知；这**并不**表示每个 go-mc 客户端、服务端、数据包编解码器、注册表或游戏玩法子系统都支持该版本。

## 范围

生成的基线包含从 1.0 到 26.2 的全部稳定版 Java Edition 构件：

- 103 个具名稳定版构件；
- 24 条市场版本线；
- 63 个不同的协议整数；以及
- 64 个以 `(transport, protocol)` 为键的线路配置档。

配置档数量有意多于协议整数数量。Mojang 曾复用协议 47：1.4.2 使用 Netty 之前的流，而 1.8 使用与之不兼容的 Netty 分帧。调用方不得仅用一个整数作为通用配置档键。

快照版、预发布版、候选发布版、愚人节版本、Classic、Indev、Infdev、Alpha、Beta 和 Bedrock Edition 不在本目录范围内。

## 固定输入

版本集合及 `releaseTime` 来自 Mojang 实时的 `version_manifest_v2.json`：

```text
https://piston-meta.mojang.com/mc/game/version_manifest_v2.json
```

检入的快照获取于 2026-08-26（Asia/Shanghai），当时最新稳定版为 26.2。当时完整实时响应的 SHA-256 为：

```text
df4e59679e7ef0057b558786e027ec1cc2fed54c2cec1231e182be75c68e42d5
```

协议号、传输族、主要版本线和现代 DataVersion 值来自 PrismarineJS/minecraft-data 的以下不可变提交：

```text
105097328f99a4f45cb6dca0fbef97db0cbd1cfd
```

固定的完整 `data/pc/common/protocolVersions.json` 的 SHA-256 为：

```text
43feef73789dd2c0f03cdbeda08dc3814ab236beb3dadfe12e51b0335bd13595
```

截至协议 775（26.1）的 Handshake、Status、Login、Configuration 和 Play 数据包 ID 来自同一不可变提交中的 `protocol.json` 模式。用于将版本别名解析到模式的固定完整 `data/dataPaths.json` 的 SHA-256 为：

```text
f4b7bca4fe57fd2574de3ccd08b3cd47e7d0d9857f02b92107e6edc73819438d
```

`packet_mappings.json` 记录每个已映射配置档的来源路径和完整模式 SHA-256。它包含精简的数据包名称/ID 表，而不是完整的数据包字段模式。在此提交中，minecraft-data 为 51 个 Netty 配置档中的 48 个提供了权威映射。明确缺失的是协议 4（1.7.2-1.7.5）、协议 485（1.14.2）和协议 776（26.2）。

协议 4 由独立固定的 `mojang_protocol_4_verified.json` 研究夹具补齐（SHA-256 `9712f0fc237c0894e96b32bf9feaefdd9ce337b3936681f0b53c2c2e59b18143`）。它记录了从 Mojang 官方 1.7.2 至 1.7.5 服务端 JAR 中直接提取 JVM classfile 注册表的结果，以及 1.7.6 的协议 5 边界：

```text
1.7.2 server.jar SHA-1: 3716cac82982e7c2eb09f83028b555e9ea606002
1.7.3 server.jar SHA-1: 707857a7bc7bf54fe60d557cca71004c34aa07bb
1.7.4 server.jar SHA-1: 61220311cef80aecc4cd8afecd5f18ca6b9461ff
1.7.5 server.jar SHA-1: e1d557b2e31ea881404e41b05ec15c810415e060
1.7.6 边界 server.jar SHA-1: 41ea7757d4d7f74b95fc1ac20f919a8e521e910c
```

该夹具固定了每个元数据 URL、JAR SHA-1/SHA-256、提取出的类条目名称/角色/SHA-256、提取工具以及固定提交中的规范名称来源。生成器会验证全部来源信息和精确的分帧数量：1 个 Handshake、4 个 Status、5 个 Login、65 个客户端方向 Play，以及 24 个服务端方向 Play。它还会验证 Login Success ID 2 后紧跟 Play Join Game（`login`）ID 1。

官方 p4 注册表独立证明其 ID/语义顺序与 p5 相同，生成器要求两者完全相等作为佐证。它并非通过复制 p5 推导 p4。两者的负载并非始终相同：UUID 写法和 Spawn Player 字段在 p5 发生了变化。旧版 `0xFE` ping 是特殊的非分帧路径，因此有意从每个已分帧的 `Profile` map 中排除。

协议 485 由 go-mc 自身不可变的 v1.14.2 历史记录补齐。该标签声明 `ProtocolVersion = 485`；`data/packetIDs.go` 包含完整的客户端方向与服务端方向 Play `iota` 表，而 `bot/mcbot.go` 和 `bot/login.go` 则给出该客户端使用的生命周期 ID：

```text
仓库：https://github.com/Tnze/go-mc
标签：v1.14.2
提交：ff7439c28ed8c36720363bec67879c980c880ba0c
data/packetIDs.go Git blob：7ea34b3b361ee7c38e04bee524df0c43d7299de4
bot/mcbot.go Git blob：9abf86668cd075187778d9f5affc93d62fe5b5d3
bot/login.go Git blob：731fb28802eda936624bef0e7fcc6325c4701193
```

数据包 ID blob 在 v1.14.1 和 v1.14.3 标签中完全相同。精简的历史夹具将每个原始 Go 标识符保留为 `sourceKind`。其公开的 `kind` 拼写会同时对照固定的协议 480 和协议 490 模式进行检查；ID 始终来自协议 485 的历史表，而不是相邻版本的副本。旧客户端没有实现服务端方向的 Login Plugin Response，因此该身份保持缺失，不进行猜测。

协议 776 独立使用 Mojang 官方 26.2 服务端数据生成器补齐，并非从 26.1 推断。检入报告的来源信息如下：

```text
版本元数据：https://piston-meta.mojang.com/v1/packages/f9e9e12c8b96ea9e04b7f87e36583a6ba0e2e9e6/26.2.json
server.jar SHA-1: 823e2250d24b3ddac457a60c92a6a941943fcd6a
生成命令：java -DbundlerMainClass=net.minecraft.data.Main -jar server-26.2.jar --reports
generated/reports/packets.json SHA-256: 02ab88951f242b28cc91e77c9f05815982b155ce8ab8b93af54aab48f4d4cb57
```

仓库内置的 `mojang_26_2_packets.json` 在 Mojang 生成的字节末尾添加了一个换行，以遵循仓库文本文件约定。生成器会规范化检出后的行尾，并在验证权威校验和之前恰好移除最后这个换行。它只会规范化经过审查的 Mojang 与 minecraft-data 生命周期名称等价项；除此之外，Mojang Play 数据包资源路径均保持不变。

26.2 的 Login Success 负载边界也由 Mojang 官方构件直接核对。26.1.2（协议 775）的 `ClientboundLoginFinishedPacket` 构造器和组合式 `STREAM_CODEC` 只包含 `GameProfile`；26.2（协议 776）的同名 record 变为 `(GameProfile gameProfile, UUID sessionId)`，编码顺序为 `ByteBufCodecs.GAME_PROFILE` 后接 `UUIDUtil.STREAM_CODEC`，因此线路末尾恰好新增 16 字节。26.2 的 `ServerLoginPacketListenerImpl` 从 `ServerConnectionListener.getSessionId()` 取得该值；后者为服务端监听器惰性生成并复用 `UUID.randomUUID()`。官方客户端将收到的 `sessionId` 交给遥测世界会话管理器，不参与登录身份验签。用于确认协议边界的补充构件如下：

```text
26.1.2 版本元数据：https://piston-meta.mojang.com/v1/packages/5b584750df77780aa8d69e186d6f4a8b7a37ce8c/26.1.2.json
26.1.2 server.jar SHA-1: 97ccd4c0ed3f81bbb7bfacddd1090b0c56f9bc51
26.2 client.jar SHA-1: 2dc72797acbc1b63fc16a11c4ac393605f453754
26.1.2 ClientboundLoginFinishedPacket.class SHA-256: c6782b6df83d643c853b77a415235cd479da5403bc6ce654eb418152e8cc2874
26.2 ClientboundLoginFinishedPacket.class SHA-256: 97a7a33e418ad7b56ad58b1dfd1f8023c54ec3f73791bb2abcba2a3ecc74d942
数据包类：net.minecraft.network.protocol.login.ClientboundLoginFinishedPacket
服务端会话类：net.minecraft.server.network.ServerConnectionListener
客户端处理类：net.minecraft.client.multiplayer.ClientHandshakePacketListenerImpl
```

生成器在 `internal/generateprotocol/sources/` 下嵌入精简且便于审查的快照。精简结果保留构建目录所用的每个来源字段和完整的数据包名称/ID map，但省略数据包字段模式及无关的开发元数据。

## 明确的协调规则

生成器应用三个经过审查的例外规则，并对任何其他缺失映射采取失败关闭策略：

1. Mojang 目前将最终版 `1.5` 构件标记为 `snapshot`；它是稳定版本集合中唯一获准进入的非 `release` 清单条目。
2. Mojang 将第一个最终版构件命名为 `1.0`，而 minecraft-data 将其命名为 `1.0.0`；仅对来源查找名称设置别名。
3. minecraft-data 没有精确的 `1.7.3` 行。固定的官方 1.7.2-1.7.5 JAR 注册表证据表明它属于同一个 `netty/4` 线路配置档。其 DataVersion 仍然未知。

早期版本不存在 Mojang DataVersion。minecraft-data 中小于 100 的值是历史排序标记，而不是官方世界数据版本，因此目录为这些版本公开 `UnknownDataVersion`（`-1`）。

版本按解析后的 `releaseTime` 排序。当 Mojang 提供相同时间戳时（尤其是 1.4.5 和 1.4.6），使用数字版本顺序作为确定性的平局判定规则。

## Configuration 模式证据

p764-p775 的 Configuration 字段边界使用上述固定 minecraft-data 提交中的协议模式；p776 ID 使用 Mojang 官方 26.2 数据包报告。必需的请求/回复行为通过 Mojang 客户端和服务端处理器进行交叉检查，而不是根据相邻数据包 ID 推断。

有一个固定的上游模式错误未被有意复现。minecraft-data 1.21.8（协议 772）的 Cookie Response 将其值描述为裸 ByteArray。Mojang 官方映射后的客户端字节码会调用 `readNullable`/`writeNullable`，因此实际线路结构仍为 Identifier、Boolean 和可选 ByteArray，与相邻版本一致。证据元数据如下：

```text
版本元数据：https://piston-meta.mojang.com/v1/packages/1873c42e50571bfca4553f980f5ff0f334e78068/1.21.8.json
client.jar SHA-1: a19d9badbea944a4369fd0059e53bf7286597576
客户端映射 SHA-1：bdeb624c3aefba11d9d40f34bc96176350b549b6
映射后的数据包类：net.minecraft.network.protocol.common.ServerboundCookieResponsePacket
```

## 生成器命令

常规生成完全离线，并使用检入的来源快照：

```sh
go generate ./protocol
# 等价命令：
go run ./internal/generateprotocol
```

可复现性检查同样离线运行，且不会写入文件：

```sh
go run ./internal/generateprotocol -check
```

若要检查 Mojang 新发布的稳定版，请获取实时清单，以及固定 minecraft-data 提交中的协议映射、数据路径和数据包模式。这是一项只读漂移检查：

```sh
go run ./internal/generateprotocol -check -online
```

若要主动刷新精简快照和生成的目录：

```sh
go run ./internal/generateprotocol -update
```

`-update` 是唯一既访问网络又重写 minecraft-data 衍生来源快照的模式。p4 字节码研究夹具、p485 项目历史夹具和 Mojang 26.2 报告是分别审查的输入。请一并审查来源差异和生成结果差异。如果新的 Mojang 版本在固定的 minecraft-data 提交中尚无对应数据，更新会失败，而不会猜测协议。

## 数据包身份策略

数据包身份表不是数据包编解码器，也不会提升能力等级。它们让调用方能够在特定 `(transport, protocol, state, direction)` 范围内，将语义数据包种类解析为正确的数字 ID。所有可用的 Play 数据包 ID 都会保留，因为选择性表格往往会隐藏版本边界；这仍不表示 go-mc 能够编码或解码其负载。

目前全部 51 个 Netty 配置档都具有经过审计的分帧身份 map。旧版 `0xFE` ping 等特殊非分帧兼容性探测保留在传输实现中，而不会作为普通 Profile 数据包 ID 出现。

## 能力策略

当前所有目录条目都将状态支持声明为 `Experimental`。在全部 51 个配置档均具备客户端/服务端状态切换与语义分发矩阵后，每个 Netty 配置档都将 Login 和原始 `PlayCore` 声明为 `Experimental`。协议 764 至 776 还将中间的 Configuration 状态声明为 `Experimental`；阻塞式回复已经实现，而非阻塞注册表/世界负载在 p767 深度解码基线之外仍可能保持不透明。这些等级表示连接可以进入 Play，并按稳定的语义身份公开已分帧数据包。它们并不声称具备每个 Play 数据包的负载编解码器。在独立的编解码器和 Vanilla 端到端测试足以证明可以提升等级之前，Chat、Inventory、World 和完整 Server 支持仍为 `Unsupported`。
