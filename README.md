# metafusion-importer — MetaFusion 导入器插件

把外部来源（Bangumi 等）的条目按**层级**抓下来，以**某个用户的身份**写入 [MetaFusion](https://github.com/MoeclubM/MetaFusion) 元数据目录，并把整次导入记成一个**可查看、可回退、可评论的批次**。

它是一个**独立插件**：不 import 目录服务的内部包、不直连数据库，只通过 `/api` 调用目录与账号服务。因此目录侧的所有校验（证据、词表、结构归属、无环、乐观并发）对它一视同仁，它在 revisions 里留下的就是那个用户的一次编辑。

## 怎么抓：层级规则

给定根实体 A 与层级 N：

| 到根的跳数 | 处理方式 |
| --- | --- |
| `< N` | **完整导入**：建实体，并与同样完整导入的节点建关系 |
| `= N` | **只建基础信息**：实体建出来，但不建任何关系 |
| `> N` | 本次不处理 |

以层级 2 抓 A 为例，存在链条 A-B-C 时：**完整添加 A 与 B（含 A-B 关系），C 只建基础信息且不建关系**。理由：C 的其余链条还没进来，此时连关系会留下指向缺失项目的悬挂边；等下次以 C 为根抓取时再补全它的关系。

每次运行都会打印计划，说明"哪些完整导入、哪些只建基础信息、哪条关系因为一端到了层级边界而被丢弃"（`plan` 子命令可离线预览）。

## 怎么记账：批次（改动集）

一次导入 = 一个批次，落在 `MF_IMPORT_LOG_DIR`（默认 `./import-logs`）下的一个 JSON 里：

- **操作记录**：逐条记 `create_entity / create_relation / update_entity`，含来源 id、目录 id、成功/失败/跳过与目录返回的错误码；
- **回退**：`rollback` 生成**逆序**补偿动作 —— 新建的关系删除、新建的实体走 lifecycle 停用、更新过的实体用更新前快照写回；默认只打印计划，`-apply` 才执行；
- **评论**：`comment` 给批次写说明（为什么这么导、哪里存疑），随批次一起保存，`show` 时可见；
- 多个批次可以同时存在：账本按批次分文件，列表按时间倒序，互不影响。

## 限速

抓来源与写目录各有独立令牌桶：`MF_RATE_FETCH`（默认 1/秒，照顾来源站点）、`MF_RATE_WRITE`（默认 5/秒，别把目录打满）。设为 0 表示不限速。

## 用法

~~~sh
# 只算计划（离线，不写任何东西）
mf-importer plan -in examples/bangumi-sample.json -root A -depth 2

# 执行导入（--dry-run 只演算不写入）
export MF_API=https://findverse.cc
export MF_TOKEN=<账号服务签发的用户令牌>
mf-importer run -in examples/bangumi-sample.json -root A -depth 2 \
  -actor curator -note "示例来源图首次导入（层级 2）" -log ./import-logs

# 查看 / 评论 / 回退
mf-importer batches -log ./import-logs
mf-importer show    -log ./import-logs -id import-20260915T041500-12
mf-importer comment -log ./import-logs -id import-20260915T041500-12 -author curator -body "C 的日期待核实"
mf-importer rollback -log ./import-logs -id import-20260915T041500-12          # 只看计划
mf-importer rollback -log ./import-logs -id import-20260915T041500-12 -apply   # 真正执行
~~~

## 状态与移植计划

已实现并带测试：

- `internal/ratelimit`：令牌桶限速（含假时钟测试）；
- `internal/graph`：层级规划（含"层级 2 下 A-B 完整、C 只建基础信息"的用例、环与重复边安全）；
- `internal/batch`：批次账本（操作记录、逆序回退计划、评论、按批次落盘与列表）；
- `internal/catalog`：目录 API 客户端（真实信封 + 幂等键 + 错误码透传，含 httptest 用例）。

**尚未完成**（明确列出，避免误以为已经能跑 Bangumi）：

1. **来源映射**：`internal/bangumi` 还没写。现有映射逻辑在目录主仓库的 `backend/internal/catalog/importer.go`（3723 行，含 Bangumi 抓取、字段映射、去重与幂等），下一步把它**移植**成"产出 `sourceGraph` JSON"的来源包 —— 本仓库的 CLI 已经把 `sourceGraph` 定为来源与执行之间的接口，所以移植不阻塞其它部分。
2. **主仓库退役 `/api/importer/*`**：插件成为权威实现后，目录侧那两个端点应下线（先并行一段时间做结果对比）。
3. **前端**：批次列表/详情/回退/评论的界面（现在只有 CLI）。
4. **部署**：compose 增加插件服务与账本卷；定时抓取（按来源速率与站点队列）。

## 验证

~~~sh
go vet ./... && go test ./... -count=1 && go build ./...
~~~
