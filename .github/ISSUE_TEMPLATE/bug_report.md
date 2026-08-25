---
name: 错误报告
about: 报告此库中的异常行为
title: ""
labels: bug
assignees: ""
---

## 问题描述

请描述实际发生的情况以及你期望的结果。

## 环境

- go-mc 提交或模块版本：
- Go 版本和操作系统：
- Minecraft Java 发布版本名称：
- 传输方式和协议号：
- 受影响的状态/子系统（Status、Login、Configuration、Play、Chat、
  Inventory、World 或 Server）：

## 复现方式

```go
package main

// import "github.com/imfusheng/go-mc/..."

func main() {}
```

请提供最小可复现程序、完整错误输出，以及已移除凭据、访问令牌、服务器地址和
UUID 的数据包跟踪记录。

## 能力报告

请粘贴以下命令输出中对应的行：

```sh
go run github.com/imfusheng/go-mc/examples/mccompat@master
```

## 补充信息

请说明对端是原版、代理还是经过模组修改的服务端/客户端，并提供其准确版本。
