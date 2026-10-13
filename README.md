# metafusion-importer

> **写目录路径已删除（第 ⑤ 步）**  
> 外部来源只允许只读查询：MetaFusion `POST /api/external-sources/query`（MCP 工具 `external_source_query`）。  
> 本仓库不再把外部数据写入目录库；`run` / `rollback -apply` 已移除。

仍可用的离线子命令（本地账本，不访问目录 API）：

```bash
mf-importer plan -in examples/bangumi-sample.json -root A -depth 2
mf-importer batches -log ./import-logs
mf-importer show -log ./import-logs -id <batch-id>
mf-importer comment -log ./import-logs -id <batch-id> -author curator -body "备注"
```

请用正常 commit 工作流（证据 + `source_refs`）人工/Agent 写入目录。
