# NBT [![Go 文档](https://pkg.go.dev/badge/github.com/imfusheng/go-mc/nbt.svg)](https://pkg.go.dev/github.com/imfusheng/go-mc/nbt)

本包实现了 Minecraft 的[命名二进制标签（Named Binary Tag）](https://minecraft.wiki/w/Minecraft_Wiki%3AProjects/wiki.vg_merge/NBT)格式。

其 API 与标准库 `encoding/json` 非常相似，但修复了其中的一些问题。
如果你使用过 `encoding/json`（这很常见），那么本包也很容易上手。

## 支持的结构体标签和选项

- `nbt` - 主要标签名，详见下文。
- `nbtkey` - 字段的键名（用于支持标签名中的逗号 `,`）

### `nbt` 标签

在大多数情况下，只需要使用它来指定标签名称。

`nbt` 结构体标签的格式为：`<nbt tag>[,opt]`。

它是一个以逗号分隔的选项列表。
第一项是标签名称，其余各项为选项。

示例：
```go
type MyStruct struct {
    Name string `nbt:"name"`
}
```

如果不希望编码器对某个字段进行编码，请使用 `-`：
```go
type MyStruct struct {
    Internal string `nbt:"-"`
}
```

如果希望编码器在字段为零值时跳过该字段，请使用 `omitempty`：
```go
type MyStruct struct {
    Name string `nbt:"name,omitempty"`
}
```

默认情况下，类型为 `[]byte`、`[]int32` 和 `[]int64` 的字段将分别编码为 `TagByteArray`、`TagIntArray` 和 `TagLongArray`。
可以使用 `list` 覆盖此行为，指定将它们编码为 `TagList`：
```go
type MyStruct struct {
    Data []byte `nbt:"data,list"`
}
```

### `nbtkey` 标签

JSON 标准库的一个常见问题是：无法为结构体指定包含逗号的键。
（例如 `{"a,b" : "c"}`）

因此，可以使用以下变通方式：

```go
type MyStruct struct {
    AB string `nbt:",omitempty" nbtkey:"a,b"`
}
```
