// Package batch 是导入的"改动集"：一次抓取产生的所有写入算一个批次，
// 有操作记录、可回退、可评论 —— 相当于给导入过程一个类似提交（commit）的单位。
//
// 为什么需要它：目录侧已经把每次写入记进 revisions（谁、何时、为何、来源），
// 但"这一千次写入属于同一次抓取"这层信息只有导入器自己知道。落成批次账本之后：
//
//   - 可以按批次查看"这次到底动了什么"；
//   - 可以按批次回退（逐条生成补偿动作，逆序执行）；
//   - 可以在批次上写评论（为什么这么导、哪里存疑），供接手的人看。
//
// 账本是插件自己的日志格式：JSON 字段用 Go 字段名（没有额外标签），不承诺对外稳定；
// 实体内容仍以目录侧的 revisions 为准，这里只留回退所需的更新前快照。
package batch

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// OpKind 是批次里的操作种类。
type OpKind string

const (
	OpCreateEntity   OpKind = "create_entity"
	OpUpdateEntity   OpKind = "update_entity"
	OpCreateRelation OpKind = "create_relation"
)

// Op 是一条操作记录。
type Op struct {
	Seq        int            // 批次内自增序号
	Kind       OpKind         // 操作种类
	SourceRef  string         // 来源侧标识（如 Bangumi id）
	Label      string         // 人可读说明（题名 / 关系码）
	EntityID   string         // 目录侧实体 id（写入成功后填）
	RelationID string         // 目录侧关系 id
	Before     map[string]any // 更新前快照（回退用）
	Status     string         // ok / failed / skipped
	Err        string         // 失败原因（目录返回的错误码）
	At         time.Time
}

// Comment 是人对批次留下的说明。
type Comment struct {
	Author string
	Body   string
	At     time.Time
}

// Source 是写入时随请求提交的证据，形状与目录 API 的 sources 一致。
type Source struct {
	Kind     string
	Citation string
	URL      string
}

// Batch 是一次导入的账本。
type Batch struct {
	ID         string   // import-<时间>-<根 id>
	Source     string   // bangumi / …
	Root       string   // 来源侧根 id
	RootTitle  string   // 根实体题名（列表里看得懂）
	Depth      int      // 本次抓取层级
	Actor      string   // 以哪个用户身份提交（目录按该身份判权限）
	Note       string   // 提交说明（作为每次写入的 edit_note）
	Sources    []Source // 证据（随每次写入提交）
	DryRun     bool     // 只演算不写入
	CreatedAt  time.Time
	UpdatedAt  time.Time
	Ops        []Op
	Comments   []Comment
	RolledBack string // 回退时间（RFC3339），空表示未回退
}

// New 建一个批次。id 由调用方给定（便于人肉辨认与幂等）。
func New(id, source, root, rootTitle, actor, note string, depth int, sources []Source) *Batch {
	now := time.Now().UTC()
	return &Batch{ID: id, Source: source, Root: root, RootTitle: rootTitle, Depth: depth,
		Actor: actor, Note: note, Sources: sources, CreatedAt: now, UpdatedAt: now}
}

// Add 追加一条操作记录，序号自增。
func (b *Batch) Add(op Op) {
	op.Seq = len(b.Ops) + 1
	if op.At.IsZero() {
		op.At = time.Now().UTC()
	}
	b.Ops = append(b.Ops, op)
	b.UpdatedAt = time.Now().UTC()
}

// Comment 在批次上留一条评论。
func (b *Batch) Comment(author, body string) {
	b.Comments = append(b.Comments, Comment{Author: author, Body: body, At: time.Now().UTC()})
	b.UpdatedAt = time.Now().UTC()
}

// Action 是一条回退动作。
type Action struct {
	Kind       string // delete_relation / retire_entity / restore_entity
	EntityID   string
	RelationID string
	Before     map[string]any
	Reason     string
}

// RollbackPlan 生成回退动作：逆序执行，只针对成功的操作。
//
//   - 新建的关系 → 删除该关系；
//   - 新建的实体 → 交给目录 lifecycle 停用（不物理删数据，保留审计）；
//   - 更新过的实体 → 用 Before 快照写回更新前的样子。
//
// 失败与被跳过的操作本来就没改到数据，不进计划。
func (b *Batch) RollbackPlan() []Action {
	var out []Action
	for i := len(b.Ops) - 1; i >= 0; i-- {
		op := b.Ops[i]
		if op.Status != "ok" {
			continue
		}
		switch op.Kind {
		case OpCreateRelation:
			out = append(out, Action{Kind: "delete_relation", RelationID: op.RelationID,
				Reason: fmt.Sprintf("回退批次 %s：删除新建的关系 #%d", b.ID, op.Seq)})
		case OpCreateEntity:
			out = append(out, Action{Kind: "retire_entity", EntityID: op.EntityID,
				Reason: fmt.Sprintf("回退批次 %s：停用新建的实体 #%d", b.ID, op.Seq)})
		case OpUpdateEntity:
			out = append(out, Action{Kind: "restore_entity", EntityID: op.EntityID, Before: op.Before,
				Reason: fmt.Sprintf("回退批次 %s：还原更新的实体 #%d", b.ID, op.Seq)})
		}
	}
	return out
}

// Summary 给出人可读统计（列表与评论里都用它）。
func (b *Batch) Summary() string {
	var ok, failed, skipped int
	for _, op := range b.Ops {
		switch op.Status {
		case "ok":
			ok++
		case "failed":
			failed++
		case "skipped":
			skipped++
		}
	}
	return fmt.Sprintf("%s → %s（%s，层级 %d）：成功 %d / 失败 %d / 跳过 %d",
		b.Source, b.RootTitle, b.Root, b.Depth, ok, failed, skipped)
}

// Store 是批次账本的存储：一个批次一个 JSON 文件，放在同一个目录里。
//
// 用文件而不是数据库：账本是导入器的本地记录（每天几次量级），不值得再引入一套存储依赖；
// 需要集中管理时把目录挂到共享卷即可。
type Store struct{ Dir string }

func (s Store) path(id string) string {
	return filepath.Join(s.Dir, strings.ReplaceAll(id, "/", "_")+".json")
}

// Save 落盘：先写临时文件再改名，避免留下半截账本。
func (s Store) Save(b *Batch) error {
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path(b.ID) + ".tmp"
	if err := os.WriteFile(tmp, append(data, byte(10)), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path(b.ID))
}

// Load 读一个批次。
func (s Store) Load(id string) (*Batch, error) {
	data, err := os.ReadFile(s.path(id))
	if err != nil {
		return nil, err
	}
	var b Batch
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, err
	}
	return &b, nil
}

// List 按创建时间倒序列出全部批次；损坏的文件跳过，不因为一个坏文件看不到其余记录。
func (s Store) List() ([]*Batch, error) {
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []*Batch
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := s.Load(strings.TrimSuffix(e.Name(), ".json"))
		if err != nil {
			continue
		}
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}
