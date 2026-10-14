# metafusion-importer

> **写目录路径已删除（第 ⑤ 步）**  
> 外部来源只允许只读查询：MetaFusion `POST /api/external-sources/query`（MCP 工具 `external_source_query`）。  
> 本仓库不再把外部数据写入目录库；`run` / `rollback -apply` 已移除。

仅保留离线图规划与历史批次账本。目录写客户端兼容空壳及未使用的抓取/写库限速器已删除；不会发出目录写请求。现有只读来源适配器由主服务 `/api/external-sources` 提供，编目写入使用常规 commit 工作流。

仍可用的离线子命令（本地账本，不访问目录 API）：

```bash
mf-importer plan -in examples/bangumi-sample.json -root A -depth 2
mf-importer batches -log ./import-logs
mf-importer show -log ./import-logs -id <batch-id>
mf-importer comment -log ./import-logs -id <batch-id> -author curator -body "备注"
```

请用正常 commit 工作流（证据 + `source_refs`）人工/Agent 写入目录。
