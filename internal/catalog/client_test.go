package catalog

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MoeclubM/metafusion-importer/internal/batch"
)

// 创建实体必须发真实信封（entity + expected_version=0 + edit_note + sources），
// 并带上令牌与幂等键 —— 这一条守住"插件与人工编辑走同一套校验"的前提。
func TestCreateEntitySendsRealEnvelope(t *testing.T) {
	var got map[string]any
	var path, auth, idem string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		auth = r.Header.Get("Authorization")
		idem = r.Header.Get("Idempotency-Key")
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("载荷不是 JSON：%v", err)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"e1","kind":"work","version":1}`))
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL, Token: "test-token"}
	e, err := c.CreateEntity(context.Background(),
		map[string]any{"kind": "work", "title": "某作品"},
		"首次导入：Bangumi 12", []batch.Source{{Kind: "url", Citation: "Bangumi 12", URL: "https://bgm.tv/subject/12"}})
	if err != nil {
		t.Fatal(err)
	}
	if path != "/api/catalog/entities" {
		t.Fatalf("路径应为 /api/catalog/entities，实际 %s", path)
	}
	if auth != "Bearer test-token" {
		t.Fatalf("令牌未带上：%q", auth)
	}
	if idem == "" {
		t.Fatal("创建请求应带 Idempotency-Key")
	}
	if v, _ := got["expected_version"].(float64); v != 0 {
		t.Fatalf("创建时 expected_version 必须为 0，实际 %v", got["expected_version"])
	}
	if got["edit_note"] != "首次导入：Bangumi 12" {
		t.Fatalf("edit_note 未提交：%v", got["edit_note"])
	}
	srcs, ok := got["sources"].([]any)
	if !ok || len(srcs) != 1 {
		t.Fatalf("sources 未提交：%v", got["sources"])
	}
	if _, ok := got["entity"].(map[string]any); !ok {
		t.Fatalf("entity 未包在信封里：%v", got)
	}
	if e.ID != "e1" || e.Version != 1 {
		t.Fatalf("响应解析不符：%+v", e)
	}
}

// 目录的错误码必须能被上层拿到（写进批次账本的 failed 记录里）。
func TestAPIErrorCarriesCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"translation_required"}`))
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL, Token: "t"}
	_, err := c.CreateEntity(context.Background(), map[string]any{"kind": "work"}, "n", nil)
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("应返回 *APIError，实际 %T：%v", err, err)
	}
	if apiErr.Code != "translation_required" || apiErr.Status != 400 {
		t.Fatalf("错误码未透传：%+v", apiErr)
	}
}

// 没有令牌时必须直接失败，而不是以匿名身份发出去（否则会在目录侧变成 401，掩盖"忘了配令牌"）。
func TestMissingTokenFailsFast(t *testing.T) {
	c := &Client{BaseURL: "http://127.0.0.1:1"}
	if _, err := c.CreateEntity(context.Background(), map[string]any{"kind": "work"}, "n", nil); err == nil {
		t.Fatal("缺令牌应报错")
	}
}
