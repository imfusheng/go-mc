# go-mc

[![CI](https://github.com/imfusheng/go-mc/actions/workflows/go.yml/badge.svg)](https://github.com/imfusheng/go-mc/actions/workflows/go.yml)
[![CodeQL](https://github.com/imfusheng/go-mc/actions/workflows/codeql-analysis.yml/badge.svg)](https://github.com/imfusheng/go-mc/actions/workflows/codeql-analysis.yml)
[![Go 文档](https://pkg.go.dev/badge/github.com/imfusheng/go-mc.svg)](https://pkg.go.dev/github.com/imfusheng/go-mc)
[![Go 质量报告](https://goreportcard.com/badge/github.com/imfusheng/go-mc)](https://goreportcard.com/report/github.com/imfusheng/go-mc)
[![Discord](https://img.shields.io/discord/915805561138860063?label=Discord)](https://discord.gg/A4qh8BT8Ue)

这是一个面向 Minecraft Java Edition 的 Go 协议库。本分支正在将原先绑定单一版本的代码库，改造成可显式选择、可测试且不会悄然误判兼容性的多版本架构。

需要 Go 1.22 或更高版本。

## 兼容性约定

生成的版本目录覆盖 Java Edition 从 1.0 到 26.2 的所有稳定版本：共 103 个版本名称、24 条主版本发布线和 64 个不同的线路协议档案。目录中收录某一版本，并不表示该版本的所有游戏玩法编解码器均已实现。

每个档案分别公开 `Status`、`Login`、`Configuration`、`PlayCore`、`Chat`、`Inventory`、`World` 和 `Server` 的能力等级。不支持的操作会明确失败，而不会回退到最新的数据包布局。当前矩阵、定义、排除范围和验证策略见 [COMPATIBILITY.md](COMPATIBILITY.md)。基于基准测试的审计结果及按优先级排列的改进清单见 [PERFORMANCE.md](PERFORMANCE.md)。现有用户还应阅读 [MIGRATION.md](MIGRATION.md)。

当前进展：

- 支持旧式 1.0-1.6 和 Netty 1.7-26.2 系列的服务器列表 Ping；
- 已审计所有 51 个 Netty 档案（1.7.2-26.2）的数据包标识，并建立客户端和服务端从 Login 到 Play 的矩阵测试；
- 为协议 764-776 提供版本感知的 Configuration 状态，包括所有可能阻塞进入 Play 的应答；
- 为每个 Netty 档案提供档案感知的原始 Play 分发，同时保留以协议 767 为基线的现有深度数据包编解码器；
- 提供使用固定来源、可离线且可复现的协议目录生成器；
- 提供 Linux、macOS 和 Windows CI、最低 Go 版本测试、竞态检测、覆盖率、CodeQL、漏洞扫描、Dependabot、标签门控发布，以及由 EULA 明确门控的原版服务端进入 Play 测试通道。

## 安装

```sh
go get github.com/imfusheng/go-mc@master
```

本分支的新版本使用标准 Go 模块语义版本（`vX.Y.Z`）。Minecraft 兼容性通过档案矩阵报告，而不编码在模块标签中。上游历史上以 Minecraft 版本命名的标签仍仅作为历史记录保留。

## 选择协议

```go
package main

import (
	"log"

	"github.com/imfusheng/go-mc/bot"
	"github.com/imfusheng/go-mc/protocol"
)

func main() {
	profile, ok := protocol.ByName("1.16.5")
	if !ok {
		log.Fatal("未知的 Minecraft 版本")
	}

	status, delay, err := bot.PingAndListWithOptions(
		"localhost:25565",
		bot.PingOptions{Version: profile.Version()},
	)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("status=%s delay=%s", status, delay)
}
```

机器人登录时，请设置 `bot.JoinOptions.Profile`。所有 Netty 档案均可进入原始 Play 状态，但这并不表示 Chat、Inventory、World 或每一种数据包载荷编解码器都已实现。请检查 `profile.Capabilities()`，并使用 `Client.Events.AddSemanticListener` 注册跨版本的原始数据包处理器。

## 软件包

- Minecraft 网络分帧与数据包
- 机器人/客户端框架
- 服务端框架
- 双角色 RCON 协议
- JSON、NBT 和旧式 `§` 聊天消息
- NBT 和 SNBT
- 区域、区块、方块和注册表
- Yggdrasil 身份验证辅助工具

可运行的示例位于 [`examples/`](examples/) 中，例如：

```sh
go run github.com/imfusheng/go-mc/examples/mcping@master -version 1.16.5 -p 754 localhost
go run ./examples/mccompat
go run ./examples/mccompat -json
```

公共 API 仍在演进。能力等级只有在通过确定性的线路协议测试后才能提升；提升为 `Verified` 还需要具备与对应版本原版实现的互操作证据。

## 开发

```sh
go generate ./protocol
go run ./internal/generateprotocol -check
go test ./...
go vet ./...
```

在线目录漂移检查是只读的：

```sh
go run ./internal/generateprotocol -check -online
```

来源沿袭关系和审慎设计的更新流程记录在 [`protocol/SOURCES.md`](protocol/SOURCES.md) 中。

## 许可证与上游项目

本项目保留上游的 MIT 许可证。原始项目为 [`Tnze/go-mc`](https://github.com/Tnze/go-mc)；其教程和社区链接描述的版本基线可能与本项目不同。
