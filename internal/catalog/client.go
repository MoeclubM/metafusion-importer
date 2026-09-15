// Package catalog 是对元数据目录 HTTP API 的客户端。
//
// 插件**不直连数据库、也不 import 目录服务的内部包**：它就是一个普通 API 客户端，
// 用调用方给的令牌，以那个用户的身份写入。这样带来三个直接好处：
//
//   - 目录侧的所有校验（证据、词表、结构归属、无环、乐观并发）对插件一视同仁，
//     插件不可能"绕过校验"写进去；
//   - 每次写入在目录的 revisions 里就是该用户的一次编辑，审计口径与人工编辑一致；
//   - 插件可以独立部署、独立升级，目录服务不需要为它改代码。
//
// 载荷形状与线上实测一致：写入统一用信封 {entity|relation, expected_version, edit_note, sources}。
package catalog

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/MoeclubM/metafusion-importer/internal/batch"
	"github.com/MoeclubM/metafusion-importer/internal/ratelimit"
)

// Client 是目录 API 的最小客户端。
type Client struct {
	BaseURL string // 如 https://findverse.cc （末尾斜杠可有可无）
	Token   string // 账号服务签发的令牌
	HTTP    *http.Client
	// Write 限制写库速率：目录是共享资源，导入不该把它打满。
	Write *ratelimit.Limiter
}

// APIError 是目录返回的结构化错误（响应体形如 {"error":"invalid_term"}）。
type APIError struct {
	Status int
	Code   string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("catalog %d %s", e.Status, e.Code)
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (c *Client) url(path string) string {
	return strings.TrimRight(c.BaseURL, "/") + "/api" + path
}

// do 发一次请求并把错误响应转成 APIError。idempotentKey 非空时带上 Idempotency-Key：
// 目录对创建实体/关系支持 24 小时幂等，网络重试不会重复建档。
func (c *Client) do(ctx context.Context, method, path string, body any, needAuth bool, idempotentKey string) ([]byte, error) {
	if c.Write != nil {
		if err := c.Write.Wait(ctx); err != nil {
			return nil, err
		}
	}
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.url(path), reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if needAuth {
		if c.Token == "" {
			return nil, fmt.Errorf("缺少令牌：导入必须以某个用户身份提交")
		}
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	if idempotentKey != "" {
		req.Header.Set("Idempotency-Key", idempotentKey)
	}
	res, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if res.StatusCode >= 300 {
		code := strings.TrimSpace(string(data))
		var envelope struct {
			Error string
		}
		if json.Unmarshal(data, &envelope) == nil && envelope.Error != "" {
			code = envelope.Error
		}
		return nil, &APIError{Status: res.StatusCode, Code: code}
	}
	return data, nil
}

// Entity 是目录侧实体的最小视图：插件只关心 id / kind / version，其余原样透传。
type Entity struct {
	ID      string
	Kind    string
	Version int64
	Raw     map[string]any
}

// Edit 是一次写入的信封，字段名与目录 API 一致。
type Edit struct {
	Entity          map[string]any
	Relation        map[string]any
	ExpectedVersion int64
	EditNote        string
	Sources         []batch.Source
}

// CreateEntity 新建实体；expected_version 必须为 0。
func (c *Client) CreateEntity(ctx context.Context, e map[string]any, note string, sources []batch.Source) (Entity, error) {
	data, err := c.do(ctx, http.MethodPost, "/catalog/entities",
		map[string]any{"entity": e, "expected_version": 0, "edit_note": note, "sources": sources}, true, newKey())
	if err != nil {
		return Entity{}, err
	}
	return decodeEntity(data)
}

// UpdateEntity 整实体替换（目录是 PUT 不是 PATCH：调用方须先 GET 全量再改）。
func (c *Client) UpdateEntity(ctx context.Context, id string, e map[string]any, expectedVersion int64, note string, sources []batch.Source) (Entity, error) {
	data, err := c.do(ctx, http.MethodPut, "/catalog/entities/"+id,
		map[string]any{"entity": e, "expected_version": expectedVersion, "edit_note": note, "sources": sources}, true, "")
	if err != nil {
		return Entity{}, err
	}
	return decodeEntity(data)
}

// GetEntity 读一个实体（判断"已存在/可见"的唯一口径，插件不自建可见性台账）。
func (c *Client) GetEntity(ctx context.Context, id string) (Entity, error) {
	data, err := c.do(ctx, http.MethodGet, "/catalog/entities/"+id, nil, false, "")
	if err != nil {
		return Entity{}, err
	}
	return decodeEntity(data)
}

// CreateRelation 新建关系（带幂等键，重试安全）。
func (c *Client) CreateRelation(ctx context.Context, r map[string]any, note string, sources []batch.Source) (map[string]any, error) {
	data, err := c.do(ctx, http.MethodPost, "/catalog/relations",
		map[string]any{"relation": r, "expected_version": 0, "edit_note": note, "sources": sources}, true, newKey())
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// DeleteRelation 删除关系（回退用）。
func (c *Client) DeleteRelation(ctx context.Context, id, note string, sources []batch.Source) error {
	_, err := c.do(ctx, http.MethodDelete, "/catalog/relations/"+id,
		map[string]any{"edit_note": note, "sources": sources}, true, "")
	return err
}

// Lifecycle 走生命周期端点（停用/合并必须走它，普通 PUT 会被 use_lifecycle_endpoint 拒掉）。
func (c *Client) Lifecycle(ctx context.Context, id, status, note string, sources []batch.Source) error {
	_, err := c.do(ctx, http.MethodPost, "/catalog/entities/"+id+"/lifecycle",
		map[string]any{"status": status, "edit_note": note, "sources": sources}, true, "")
	return err
}

func decodeEntity(data []byte) (Entity, error) {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return Entity{}, err
	}
	// 有些端点把实体包在 entity 里返回，这里两种都认。
	if inner, ok := raw["entity"].(map[string]any); ok {
		raw = inner
	}
	e := Entity{Raw: raw}
	if id, ok := raw["id"].(string); ok {
		e.ID = id
	}
	if kind, ok := raw["kind"].(string); ok {
		e.Kind = kind
	}
	if v, ok := raw["version"].(float64); ok {
		e.Version = int64(v)
	}
	return e, nil
}

// newKey 生成幂等键。
func newKey() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprint(time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}
