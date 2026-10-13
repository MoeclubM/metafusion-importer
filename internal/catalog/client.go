// Package catalog 曾是对元数据目录 HTTP API 的写客户端。
//
// 写目录路径已删除（第 ⑤ 步）。外部来源只读查询请直接调用 MetaFusion
// POST /api/external-sources/query，不要再经本包写库。
package catalog

import "fmt"

// ErrWritesDeleted 表示本插件的目录写路径已移除。
var ErrWritesDeleted = fmt.Errorf("catalog_writes_deleted")

// Client 保留空壳，避免旧 import 断编译；任何写方法均返回 ErrWritesDeleted。
type Client struct {
	Base  string
	Token string
}

type Entity struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Version int64  `json:"version"`
}

func (c *Client) CreateEntity(ctx any, e map[string]any, note string, sources any) (Entity, error) {
	return Entity{}, ErrWritesDeleted
}
