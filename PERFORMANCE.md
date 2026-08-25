# 性能审计

审计快照：2026-08-26，`codex/multiversion-ci`，Go 1.26.6。

本次审计涵盖数据包分帧、机器人并发与所有权、NBT、注册表、区块以及生成的协议数据。本文是一份诊断报告和按优先级排序的整改计划，并不表示下列每个未决问题都已修复。

审计发现的一个正确性问题已立即修复：内置 KeepAlive 和 Ping 处理器不会再把已经归还缓冲池的借用接收缓冲区放入队列。其余发现特意在开展大范围性能改动之前记录下来。

## 方法与限制

测量环境为 Windows/amd64、AMD Ryzen 9 7900X 和 Go 1.26.6。基准测试采用一秒采样，并重复三至五次。压缩数据包解码和 SNBT 编码收集了 CPU 与内存分配性能剖析数据；包启动工作使用了 `GODEBUG=inittrace=1`。Linux CI 上的精确耗时会有所不同，但分配次数与热点排序足够稳定，可以指导第一轮优化。

由于当前 C 工具链不支持 amd64，本地 Windows 工具链无法运行竞态检测器。Linux CI 的竞态任务仍是权威检查。生成的性能剖析文件和探测二进制文件不会提交。

新的解包与队列基准测试位于 [`net/packet/packet_test.go`](net/packet/packet_test.go) 和 [`net/queue/queue_test.go`](net/queue/queue_test.go)。

## 基线结果

| 操作 | 耗时 | Bytes/op | Allocs/op |
| --- | ---: | ---: | ---: |
| 打包，64 字节负载，禁用压缩 | 66-72 ns | 58 | 3 |
| 打包，64 字节负载，已压缩 | 5.7-6.2 us | 66-75 | 3 |
| 解包，64 字节负载，禁用压缩 | 88-91 ns | 90 | 5 |
| 解包，64 字节负载，已压缩 | 6.2-8.5 us | 40,736 | 11 |
| 链表队列，`packet.Packet` 往返 | 52-61 ns | 80 | 2 |
| 通道队列，`packet.Packet` 往返 | 21-23 ns | 0 | 0 |
| 构造 `bot.NewClient` | 通常为 33-53 us | 239,417 | 74 |
| 构造空的 `registry.NewNetworkCodec` | 43-51 us | 234,672 | 66 |
| Gzip 包装的 NBT `bigTest` 端到端 | 20-22 us | 43,432 | 143 |
| 编码 SNBT `bigTest` | 49-50 us | 17,584 | 1,182 |
| 扫描同一 SNBT 但不编码 | 13-14 us | 0 | 0 |
| 调色板稳态 Get/Set | 6/8-13 ns | 0 | 0 |

现有二进制 NBT 基准测试每次迭代都会创建新的 gzip reader；它是端到端基准测试，而不是纯 NBT 解码器基准测试。性能剖析显示，gzip 初始化占据了绝大多数分配字节。在修改解码器内部实现前，需要添加预先解压的 NBT 基准测试。

## 审计发现

### P0：不受信任的长度可将小帧放大成 OOM 或 panic

多个解码器会根据远端给出的计数分配内存，然后才检查该计数对所在模式是否有效：

- [`level/bitstorage.go`](level/bitstorage.go) 直接根据一个 VarInt 确定 `[]uint64` 的大小；直到之后执行 `Fix` 时才检查确切的存储长度。
- [`level/palette.go`](level/palette.go) 中的两种调色板实现都直接根据声明的调色板大小分配内存。
- [`registry/network.go`](registry/network.go) 中已知注册表的标签解码，在执行 `make([]*E, length)` 之前，既不验证外层标签数，也不验证每个标签的元素数。
- 二进制 NBT 和 dynbt 在没有共享的字节数、元素数、乘法溢出或递归深度预算的情况下分配数组和列表。参见 [`nbt/decode.go`](nbt/decode.go) 和 [`nbt/dynbt/decode.go`](nbt/dynbt/decode.go)。
- [`net/packet/util.go`](net/packet/util.go) 将两 MiB 的帧字节数上限当作每个 `pk.Array` 都适用的元素数上限。对于 80 字节的元素，一个三字节计数可在尚未读取任何元素时申请约 160 MiB 内存。

这些属于性能和可用性漏洞，而不是普通的微优化机会。应在每次分配前加入模式专用限制和共享解码器预算。网络 NBT 应继承帧的剩余预算，并且所有 `count * elementSize` 运算都必须检查溢出。

### P0：机器人队列缺少可用的背压机制

[`bot/mcbot.go`](bot/mcbot.go) 中默认的读写队列是无界链表。处理器同步执行时，socket reader 仍会不断从 TCP 读取，因此缓慢的处理器或突发区块流量会转化为堆增长，而不是产生网络背压。链表队列中的一次数据包往返还会消耗 80 字节并产生两次分配，而通道实现无需分配。

现有的有界通道也不是完整替代方案：`Push` 是非阻塞的，而接收队列一满就会关闭连接。应将两种行为替换为有界、感知 context 的阻塞环形队列，并公开队列深度/高水位指标。

Bundle 处理进一步加剧了这一问题。[`bot/ingame.go`](bot/ingame.go) 将一个 Bundle 限制为 4,096 个数据包，却没有累计字节数限制。按两 MiB 的数据包上限计算，理论上可保留近 8 GiB 的负载。应增加字节预算或流式分发，并在出现不可恢复的 Bundle 错误后中止连接。

### P0：压缩接收会为每个数据包重新创建 flate 字典

[`net/packet/packet.go`](net/packet/packet.go) 会对每个压缩帧调用 `zlib.NewReader`。amd64 基准测试中，每个 64 字节数据包分配 40,736 字节。内存分配性能剖析显示，99.3% 的分配空间来自 `compress/flate.NewReader` 和字典初始化。

受控的 `zlib.Resetter.Reset` 探测将 reader 初始化从约 6.2 us 和 39 KiB 降至约 0.28 us，且分配可忽略不计。应引入有界解码器池，并严格处理 Close/reset。这是数据包热路径中收益最高的改动。

压缩模式还会在得知包体是否压缩之前分配完整的线路帧。当接近协议上限时，除解码后的负载外，还会产生约两 MiB 的临时分配。应使用连接本地或按大小分级的暂存空间，但超大缓冲区应直接丢弃，而不是无限期保留在全局池中。

### P1：数据包输入存在可避免的读取与复制

[`net/conn.go`](net/conn.go) 将原始 socket 作为 `io.Reader` 暴露。因此，VarInt 解码每个字节都会执行一次底层读取，随后帧数据还要另行读取。在受控测试中，一个 64 字节帧使用原始 reader 时需要四次底层读取，而使用 4-KiB `bufio.Reader` 时只需一次。应添加逐连接缓冲，同时正确保持加密状态切换。

解压后，`setDecodedData` 仅仅为了移除数据包 ID 就复制整个负载。应先从 zlib 流读取 ID，再将声明的剩余字节直接读入可复用的负载缓冲区，同时保留精确长度、EOF 和尾随数据检查。

发送路径用于规避短写的 `io.Copy(bytes.NewReader(data))`，以及基于接口的 VarInt 写入，导致了其余小额分配。在完成影响更大的接收端工作后，可通过直接的短写循环和具体缓冲区辅助函数消除这些分配。

### P1：空闲客户端会急切分配大型注册表

每个 [`bot.NewClient`](bot/client.go) 都会构造十一个注册表。每个 [`registry.NewRegistry`](registry/registry.go) 都会为 256 个名称、值和存在位预留空间，即使旧协议从不进入 Configuration 状态也是如此。在 amd64 上，一个空的网络编解码器需要 234,672 字节和 66 次分配，几乎占新客户端 239,417 字节的全部。因此，一万个空闲客户端在尚无注册表内容时就会预留约 2.35 GB。

应使用惰性或零容量注册表，并根据已经验证的线路计数预留空间。目前未使用的 `indices` map 存储指向可增长切片的指针；它既增加开销，也可能保留过时的底层数组，因此应在改变增长行为之前移除或重新设计。还应清空已丢弃的切片尾部，以释放字符串和原始 NBT 值。

### P1：Configuration 缺少会话级预算

[`bot/configuration.go`](bot/configuration.go) 中的 Configuration 循环没有数据包总数、字节数、对象数或持续时间限制。未知注册表、Cookie、报告详情和资源包状态可能在各自有效的帧之间不断累积。被跳过的自定义负载也会先复制，再丢弃。

应加入 context/deadline 支持，并为数据包、解码字节、注册表条目、Cookie 和资源包设置累计预算。未知负载应通过受限 reader 排空，不应将其物化到内存中。

### P1：方块数据执行高成本的急切初始化

导入 [`level/block`](level/block/block.go) 时，会急切地解压并通过反射解码每个方块状态。在 amd64 上，初始化跟踪测得耗时 86 ms、累计分配 27.4 MB，并产生 241 万次分配。强制 GC 后，与基线进程相比，该导入仍额外保留约 2.65 MB 和 3.1 万个堆对象。

应生成紧凑的静态表示，或在首次使用方块/区块 API 时通过 `sync.Once` 加载该表。对于导入高级包但从不操作区块的状态客户端和机器人，这一点尤为重要。

### P1：区块光照掩码尺寸过大

[`level/chunk.go`](level/chunk.go) 按 4,096 个方块位置而不是 section 数量创建光照掩码，并在执行按位取反时额外分配两个完整掩码。普通的 24-section 区块会生成约 2,048 字节的掩码，而约 32 字节就已足够；同时还会设置无效的高位反向位。应按 `len(Sections)+2` 确定掩码尺寸，并屏蔽未使用的高位。

### P2：NBT/SNBT 与存档路径分配量大

当前样本的 SNBT 编码会产生 1,182 次分配。内存分配性能剖析显示，约 70.6% 的空间来自 `StringifiedMessage.TagType` 和 `MarshalNBT`，它们会反复扫描和复制同一字符串。二进制 NBT 列表解码总是创建新切片，标量暂存数组会通过接口逃逸，而 RawMessage 捕获会为每个值层叠一个缓冲区、tee reader 和嵌套解码器。

SNBT 应只执行一次扫描/转换；保留解码器暂存空间；在清空尾部的同时复用已验证的切片容量；并提供内部原始捕获路径。[`level/chunk.go`](level/chunk.go) 中的静态方块状态转换应按 StateID 缓存 NBT RawMessage，而不是反复编码和解码每个状态。

### P2：生命周期与所有权约定需要收紧

审计发现并修复了一个明确的所有权竞态：KeepAlive 和 Ping 响应在异步写队列中使用接收数据包借用的 `Data`，而 `HandleGame` 会立即将该切片归还 `sync.Pool`。现在处理器会重新编码其标量 ID，测试会覆盖原始接收存储空间，并且事件/WritePacket 的所有权规则已经形成文档。

相关的未决问题仍然存在：

- `HandleGame` 返回错误时不会关闭连接；如果调用方忘记关闭连接，reader 可能会继续向无界队列填充数据。
- 接收缓冲区没有按大小分级，因此偶发的接近上限的数据包可能增加缓冲池的保留内存。
- 公开的 `Conn.ReadPacket` 没有对应的释放操作。
- 在分发期间并发注册事件处理器会导致 slice/map 竞态。

应定义优雅排空与中止关闭的语义，使用拥有数据所有权的数据包封装，丢弃缓冲池中的超大缓冲区，并在分发前冻结 Events，或者使用写时复制快照。

### P3：全版本协议目录存在可测量的一次性成本

在 amd64 上加载全部 8,473 个语义映射会增加约 0.89 MB 活跃堆和 2,458 个堆对象，启动分配约为 1.55 MB。这对于当前兼容性目标可以接受；但如果小型纯状态二进制文件成为优先事项，生成排序表并使用二分查找可以减少 map 和启动工作。

## 建议执行顺序

1. 引入共享解码预算，并在每次 `make` 前验证远端给出的计数。
2. 替换机器人的无界队列，并加入 Bundle/Configuration 字节预算。
3. 池化/重置 zlib reader，并添加逐连接缓冲输入路径。
4. 将注册表改为惰性初始化，并移除未使用的指针索引。
5. 修正光照掩码尺寸，并缓存静态注册表/方块/区块编码。
6. 只有在落实安全限制后，才减少数据包、NBT 和 SNBT 的复制。
7. 在选定稳定 runner 和回归阈值后，添加 amd64 Linux 基准测试任务或外部基准测试仪表板。

## 复现方法

```powershell
$env:GOTOOLCHAIN = 'go1.26.6'
$env:GOARCH = 'amd64'

go test ./net/packet -run '^$' -bench '^BenchmarkPacket_' -benchmem -benchtime=1s -count=5
go test ./net/queue -run '^$' -bench 'QueueRoundTrip$' -benchmem -benchtime=1s -count=5
go test ./nbt -run '^$' -bench 'Benchmark(Decoder_Decode_bigTest|SNBT_bigTest|Encoder_WriteSNBT_bigTest)$' -benchmem -benchtime=1s -count=3
go test ./level -run '^$' -bench '^BenchmarkPaletteContainer$' -benchmem -benchtime=1s -count=3
```

性能剖析期间还发现了一个相邻的正确性问题：[`save/chunk.go`](save/chunk.go) 中的 gzip/zlib writer 路径会在未关闭或刷新压缩器的情况下返回缓冲区，其 reader 也没有关闭或设置字节预算。应将其作为独立的正确性修复处理，而不是隐藏在基准测试变更中。
