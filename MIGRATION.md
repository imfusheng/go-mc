# 迁移指南

## 导入路径

本分支是一个独立的 Go 模块。请将以下导入路径：

```text
github.com/Tnze/go-mc
```

替换为：

```text
github.com/imfusheng/go-mc
```

然后运行 `go mod tidy`。项目仍保留对原始上游仓库的致谢，其标签也仍可作为有用的历史参考；但模块声明的路径必须与用户传给 `go get` 的路径一致。

## 协议选择

`bot.ProtocolVersion` 仅为保持源代码兼容性而保留，表示旧式数字处理器的基线——协议 767（Minecraft 1.21/1.21.1）。新代码应选择生成的档案：

```go
profile, ok := protocol.ByName("1.16.5")
if !ok {
	// 未知的稳定版本。
}

client := bot.NewClient()
err := client.JoinServerWithOptions(address, bot.JoinOptions{Profile: profile})
```

`JoinServerWithOptions` 表示“进入原始 Play 状态”。从 1.7.2 到 26.2 的全部 51 个 Netty 档案目前均以 `Experimental` 等级提供此能力。独立的 Chat、Inventory、World 和 Server 能力仍不受支持；不要仅仅因为状态切换有效，就复用协议 767 的载荷结构体。

服务器列表 Ping 的覆盖范围更广。`bot.PingAndListWithOptions` 支持在旧式和 Netty 传输协议中显式指定目录版本。其零值选项使用 `protocol.LatestRelease()`，而不是 Play 基线。

## 能力检查

开始一项操作前，请检查相关子系统：

```go
capabilities := profile.Capabilities()
if capabilities.Inventory == protocol.Unsupported {
	// 不要使用当前的物品栏编解码器解析此档案。
}
```

不支持的布局现在会返回 `protocol.UnsupportedCapabilityError`，或包含它的更具体错误。过去依赖为旧版/新版复用最新数据包布局的代码，必须处理这一错误。

## 跨版本 Play 处理器

`Events.AddListener` 使用协议 767 的数字数据包 ID，并且仅在该基线上被调用。请按稳定的语义种类注册可移植的原始数据包处理器：

```go
client.Events.AddSemanticListener(bot.SemanticPacketHandler{
	Kind: protocol.PacketKind("keep_alive"),
	F: func(p pk.Packet) error {
		id, err := protocol.RequirePacketID(
			client.Profile, protocol.StatePlay, protocol.Serverbound,
			protocol.PacketKind("keep_alive"),
		)
		if err != nil {
			return err
		}
		// 写入操作会排队，因此回调返回后仍需保留一份副本。
		data := append([]byte(nil), p.Data...)
		return client.Conn.WritePacket(pk.Packet{ID: id, Data: data})
	},
})
```

Configuration 协议会自动发送客户端信息。服务端行为准则绝不会被隐式接受：只有在应用已经针对确切文本取得同意后，才可设置 `Client.CodeOfConduct`。传送请求和缺少同意会以类型化错误公开。内置服务端注册表编解码器仍以 p767 为基线；在对应版本的注册表编解码器可用之前，多版本服务端应实现 `server.ProfileConfigHandler`。

## 协议 767 物品栏位

1.21.1 的 `screen.Slot` 线路格式使用数据组件。`Slot.NBT` 仅作为已弃用的源代码兼容字段保留，并会在非空时被拒绝。请使用 `Slot.Components` 和 `Slot.RemovedComponents`。未知或未实现的组件载荷会返回明确错误，而不会被跳过；这是因为跳过长度未知的组件会使数据包流失步。

## 发布标签

本分支的新版本使用标准 Go 语义版本（`vX.Y.Z`）。Minecraft 兼容性记录在生成的档案/能力矩阵中，而不编码在模块标签中。当前证据等级见 [`COMPATIBILITY.md`](COMPATIBILITY.md)。
