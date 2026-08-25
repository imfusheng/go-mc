# 贡献指南

## 兼容性变更

Minecraft 版本发现、数据包身份和子系统兼容性是彼此独立的问题。新版本必须先进入生成的目录；不能仅仅因为它的协议号接近某个已知版本，就继承相应能力。

对于协议变更：

1. 在 `internal/generateprotocol/sources/` 下更新或添加固定版本且便于审查的来源；
2. 运行 `go generate ./protocol`；
3. 为每个受影响状态的两个方向添加边界测试；
4. 对不支持的布局和格式错误的长度返回明确错误；以及
5. 仅按照 `COMPATIBILITY.md` 提升能力等级。

不要手动编辑 `protocol/catalog_data.go`。

## 本地检查

```sh
go mod tidy -diff
go generate ./protocol
go run ./internal/generateprotocol -check
gofmt -w .
go vet ./...
go test ./...
```

每周运行的 CI 漂移任务会将固定的目录与 Mojang 实时稳定版清单进行比较。更新快照是一项需要主动执行且会写入文件的操作：

```sh
go run ./internal/generateprotocol -update
```

请一并审查来源快照、生成的目录、能力变更和测试。

## 提交与发布策略

使用常规 Go 模块语义化版本（`vX.Y.Z`）。Minecraft 兼容性由配置档和能力等级跟踪，而不是由模块标签跟踪。推送有效的 `v*` 标签后，只有当全部质量、最低 Go 版本、竞态、漏洞和构建检查都通过时，才会启动发布工作流。
