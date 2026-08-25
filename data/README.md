## 更新 `data`

1. 前往 [https://github.com/PrismarineJS/minecraft-data/tree/master/data/pc/{version}](https://github.com/PrismarineJS/minecraft-data/tree/master/data/pc)
2. 如果有新的对应 JSON 文件可用，请更新以下文件中的 `version`：
   - [gen_entity.go](entity/gen_entity.go) - `entities.json`
   - [gen_item.go](item/gen_item.go) - `items.json`
3. 更新 [gen_soundid.go](soundid/gen_soundid.go) 中的 `URL`（请先确认该 URL 能够返回响应）
4. 运行 `go generate ./...`
