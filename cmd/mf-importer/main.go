// mf-importer 是 MetaFusion 导入器插件的命令行入口。
//
// 子命令：
//
//	plan      只算抓取计划（离线，不发任何写请求）
//	run       执行一次导入，产出批次账本
//	batches   列出批次（改动集）
//	show      看某个批次的全部操作与评论
//	comment   在批次上写评论
//	rollback  打印回退计划；加 -apply 则真正执行
//
// 抓取来源与目录写入都通过接口注入：来源映射（Bangumi 字段 → 目录字段）由来源包提供，
// 目录写入由 internal/catalog 的客户端完成。这里只负责编排、记账与报告。
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/MoeclubM/metafusion-importer/internal/batch"
	"github.com/MoeclubM/metafusion-importer/internal/catalog"
	"github.com/MoeclubM/metafusion-importer/internal/graph"
	"github.com/MoeclubM/metafusion-importer/internal/ratelimit"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	ctx := context.Background()
	var err error
	switch os.Args[1] {
	case "plan":
		err = cmdPlan(os.Args[2:])
	case "run":
		err = cmdRun(ctx, os.Args[2:])
	case "batches":
		err = cmdBatches(os.Args[2:])
	case "show":
		err = cmdShow(os.Args[2:])
	case "comment":
		err = cmdComment(os.Args[2:])
	case "rollback":
		err = cmdRollback(ctx, os.Args[2:])
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "未知子命令：%s\n", os.Args[1])
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误："+err.Error())
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, strings.Join([]string{
		"mf-importer <子命令> [选项]",
		"",
		"  plan      -in 抓取结果.json -root <来源id> -depth <层级>",
		"  run       -in 抓取结果.json -root <id> -depth <n> -actor <用户> -note <说明> [-dry-run] [-log 目录]",
		"  batches   [-log 目录]",
		"  show      -log 目录 -id <批次id>",
		"  comment   -log 目录 -id <批次id> -author <人> -body <内容>",
		"  rollback  -log 目录 -id <批次id> [-apply]",
		"",
		"环境变量：MF_API（目录入口，如 https://findverse.cc）、MF_TOKEN（用户令牌）、",
		"          MF_IMPORT_LOG_DIR（批次账本目录，默认 ./import-logs）、",
		"          MF_RATE_FETCH / MF_RATE_WRITE（每秒请求数，默认 1 / 5）",
	}, "\n"))
}

// sourceGraph 是"抓取结果"的中间格式：来源包（Bangumi 等）负责产出它，
// 本工具负责按层级裁剪、写库与记账。它也是测试与 dry-run 的输入。
type sourceGraph struct {
	Source string `json:"source"`
	Nodes  []struct {
		SourceID string         `json:"source_id"`
		Kind     string         `json:"kind"`
		Title    string         `json:"title"`
		Entity   map[string]any `json:"entity"`
	} `json:"nodes"`
	Edges []struct {
		From     string         `json:"from"`
		To       string         `json:"to"`
		Type     string         `json:"type"`
		Relation map[string]any `json:"relation"`
	} `json:"edges"`
}

func loadGraph(path string) (*sourceGraph, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var g sourceGraph
	if err := json.Unmarshal(data, &g); err != nil {
		return nil, fmt.Errorf("抓取结果不是合法 JSON：%w", err)
	}
	return &g, nil
}

func (g *sourceGraph) plan(root string, depth int) graph.Plan {
	nodes := make([]graph.Node, 0, len(g.Nodes))
	for _, n := range g.Nodes {
		nodes = append(nodes, graph.Node{SourceID: n.SourceID, Kind: n.Kind, Title: n.Title})
	}
	edges := make([]graph.Edge, 0, len(g.Edges))
	for _, e := range g.Edges {
		edges = append(edges, graph.Edge{From: e.From, To: e.To, Type: e.Type})
	}
	return graph.Build(root, depth, nodes, edges)
}

func cmdPlan(args []string) error {
	fs := flag.NewFlagSet("plan", flag.ExitOnError)
	in := fs.String("in", "", "抓取结果 JSON")
	root := fs.String("root", "", "来源侧根 id")
	depth := fs.Int("depth", 2, "抓取层级")
	_ = fs.Parse(args)
	g, err := loadGraph(*in)
	if err != nil {
		return err
	}
	printPlan(g.plan(*root, *depth))
	return nil
}

func printPlan(p graph.Plan) {
	fmt.Printf("计划：根 %s，层级 %d\n", p.Root, p.MaxDepth)
	fmt.Printf("  完整导入（实体 + 关系）：%d 个\n", len(p.Full))
	for _, n := range p.Full {
		fmt.Printf("    [%d 跳] %s %s %s\n", n.Depth, n.Kind, n.SourceID, n.Title)
	}
	fmt.Printf("  只建基础信息（不建关系）：%d 个\n", len(p.Basic))
	for _, n := range p.Basic {
		fmt.Printf("    [层级边界] %s %s %s\n", n.Kind, n.SourceID, n.Title)
	}
	fmt.Printf("  写入的关系：%d 条；因一端超出层级而丢弃：%d 条\n", len(p.Edges), len(p.DroppedEdges))
	for _, e := range p.DroppedEdges {
		fmt.Printf("    丢弃 %s -%s-> %s（对端到了层级边界，未建关系）\n", e.From, e.Type, e.To)
	}
}

func runFlags(fs *flag.FlagSet) (in, root, actor, note, logDir, source *string, depth *int, dryRun *bool) {
	in = fs.String("in", "", "抓取结果 JSON")
	root = fs.String("root", "", "来源侧根 id")
	depth = fs.Int("depth", 2, "抓取层级")
	actor = fs.String("actor", "", "以哪个用户身份提交")
	note = fs.String("note", "", "提交说明（写入 edit_note）")
	logDir = fs.String("log", "", "批次账本目录")
	source = fs.String("source", "bangumi", "来源标识")
	dryRun = fs.Bool("dry-run", false, "只演算不写入")
	return
}

func cmdRun(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	in, root, actor, note, logDir, source, depth, dryRun := runFlags(fs)
	_ = fs.Parse(args)
	if *logDir == "" {
		*logDir = envOr("MF_IMPORT_LOG_DIR", "import-logs")
	}
	if *in == "" || *root == "" {
		return fmt.Errorf("必须给 -in 与 -root")
	}
	g, err := loadGraph(*in)
	if err != nil {
		return err
	}
	p := g.plan(*root, *depth)
	printPlan(p)
	title := ""
	for _, n := range g.Nodes {
		if n.SourceID == *root {
			title = n.Title
		}
	}
	id := fmt.Sprintf("import-%s-%s", time.Now().UTC().Format("20060102T150405"), strings.ReplaceAll(*root, "/", "_"))
	b := batch.New(id, *source, *root, title, *actor, *note, *depth, []batch.Source{{Kind: "url", Citation: *source + " " + *root}})
	b.DryRun = *dryRun

	client := &catalog.Client{
		BaseURL: envOr("MF_API", ""),
		Token:   os.Getenv("MF_TOKEN"),
		Write:   ratelimit.New(envFloat("MF_RATE_WRITE", 5), 1),
	}
	bySource := map[string]map[string]any{}
	for _, n := range g.Nodes {
		bySource[n.SourceID] = n.Entity
	}
	created := map[string]string{} // 来源 id -> 目录 id

	// 先实体后关系：只有两端都已在目录里（Full）才建关系，Basic 只建基础信息。
	for _, n := range append(append([]graph.Node{}, p.Full...), p.Basic...) {
		payload := bySource[n.SourceID]
		if payload == nil {
			b.Add(batch.Op{Kind: batch.OpCreateEntity, SourceRef: n.SourceID, Label: n.Title, Status: "skipped", Err: "抓取结果缺少实体载荷"})
			continue
		}
		if *dryRun {
			b.Add(batch.Op{Kind: batch.OpCreateEntity, SourceRef: n.SourceID, Label: n.Title, Status: "skipped", Err: "dry-run"})
			continue
		}
		e, err := client.CreateEntity(ctx, payload, *note, b.Sources)
		if err != nil {
			b.Add(batch.Op{Kind: batch.OpCreateEntity, SourceRef: n.SourceID, Label: n.Title, Status: "failed", Err: err.Error()})
			continue
		}
		created[n.SourceID] = e.ID
		b.Add(batch.Op{Kind: batch.OpCreateEntity, SourceRef: n.SourceID, Label: n.Title, EntityID: e.ID, Status: "ok"})
	}
	for _, e := range p.Edges {
		from, okFrom := created[e.From]
		to, okTo := created[e.To]
		if !okFrom || !okTo || *dryRun {
			b.Add(batch.Op{Kind: batch.OpCreateRelation, Label: e.From + "-" + e.Type + "->" + e.To, Status: "skipped", Err: "端点未建立或 dry-run"})
			continue
		}
		rel := map[string]any{"type": e.Type, "source_id": from, "target_id": to, "position": 0}
		out, err := client.CreateRelation(ctx, rel, *note, b.Sources)
		if err != nil {
			b.Add(batch.Op{Kind: batch.OpCreateRelation, Label: e.Type, Status: "failed", Err: err.Error()})
			continue
		}
		rid, _ := out["id"].(string)
		b.Add(batch.Op{Kind: batch.OpCreateRelation, Label: e.Type, RelationID: rid, Status: "ok"})
	}

	store := batch.Store{Dir: *logDir}
	if err := store.Save(b); err != nil {
		return err
	}
	fmt.Println(b.Summary())
	fmt.Println("批次账本：" + store.Dir + "/" + b.ID + ".json")
	if failed := countFailed(b); failed > 0 {
		return fmt.Errorf("%d 条操作失败，见账本与下面的错误码", failed)
	}
	return nil
}

func countFailed(b *batch.Batch) int {
	n := 0
	for _, op := range b.Ops {
		if op.Status == "failed" {
			n++
		}
	}
	return n
}

func cmdBatches(args []string) error {
	fs := flag.NewFlagSet("batches", flag.ExitOnError)
	logDir := fs.String("log", envOr("MF_IMPORT_LOG_DIR", "import-logs"), "批次账本目录")
	_ = fs.Parse(args)
	list, err := batch.Store{Dir: *logDir}.List()
	if err != nil {
		return err
	}
	if len(list) == 0 {
		fmt.Println("还没有批次。")
		return nil
	}
	for _, b := range list {
		mark := ""
		if b.RolledBack != "" {
			mark = "（已回退 " + b.RolledBack + "）"
		}
		comments := ""
		if len(b.Comments) > 0 {
			comments = fmt.Sprintf("，评论 %d 条", len(b.Comments))
		}
		fmt.Printf("%s  %s%s%s\n", b.ID, b.Summary(), comments, mark)
	}
	return nil
}

func cmdShow(args []string) error {
	fs := flag.NewFlagSet("show", flag.ExitOnError)
	logDir := fs.String("log", envOr("MF_IMPORT_LOG_DIR", "import-logs"), "批次账本目录")
	id := fs.String("id", "", "批次 id")
	_ = fs.Parse(args)
	b, err := batch.Store{Dir: *logDir}.Load(*id)
	if err != nil {
		return err
	}
	fmt.Println(b.Summary())
	fmt.Printf("提交身份：%s；说明：%s\n\n", b.Actor, b.Note)
	for _, op := range b.Ops {
		target := op.EntityID
		if target == "" {
			target = op.RelationID
		}
		fmt.Printf("  #%-4d %-16s %-28s %-8s %s %s\n", op.Seq, op.Kind, op.Label, op.Status, target, op.Err)
	}
	if len(b.Comments) > 0 {
		fmt.Println("\n评论：")
		for _, c := range b.Comments {
			fmt.Printf("  [%s] %s：%s\n", c.At.Format(time.RFC3339), c.Author, c.Body)
		}
	}
	return nil
}

func cmdComment(args []string) error {
	fs := flag.NewFlagSet("comment", flag.ExitOnError)
	logDir := fs.String("log", envOr("MF_IMPORT_LOG_DIR", "import-logs"), "批次账本目录")
	id := fs.String("id", "", "批次 id")
	author := fs.String("author", "", "评论人")
	body := fs.String("body", "", "评论内容")
	_ = fs.Parse(args)
	if *id == "" || *body == "" {
		return fmt.Errorf("必须给 -id 与 -body")
	}
	store := batch.Store{Dir: *logDir}
	b, err := store.Load(*id)
	if err != nil {
		return err
	}
	b.Comment(*author, *body)
	if err := store.Save(b); err != nil {
		return err
	}
	fmt.Printf("已记录评论（%s 现有 %d 条评论）\n", *id, len(b.Comments))
	return nil
}

func cmdRollback(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("rollback", flag.ExitOnError)
	logDir := fs.String("log", envOr("MF_IMPORT_LOG_DIR", "import-logs"), "批次账本目录")
	id := fs.String("id", "", "批次 id")
	apply := fs.Bool("apply", false, "真正执行（默认只打印计划）")
	note := fs.String("note", "回退导入批次", "回退的 edit_note")
	_ = fs.Parse(args)
	store := batch.Store{Dir: *logDir}
	b, err := store.Load(*id)
	if err != nil {
		return err
	}
	plan := b.RollbackPlan()
	if len(plan) == 0 {
		fmt.Println("没有需要回退的操作（可能全部失败/跳过，或已回退）。")
		return nil
	}
	fmt.Printf("回退计划（逆序执行，共 %d 步）：\n", len(plan))
	for i, a := range plan {
		fmt.Printf("  %2d. %-16s %s\n", i+1, a.Kind, a.Reason)
	}
	if !*apply {
		fmt.Println("\n（未执行；加 -apply 生效）")
		return nil
	}
	client := &catalog.Client{
		BaseURL: envOr("MF_API", ""),
		Token:   os.Getenv("MF_TOKEN"),
		Write:   ratelimit.New(envFloat("MF_RATE_WRITE", 5), 1),
	}
	var failed int
	for _, a := range plan {
		var err error
		switch a.Kind {
		case "delete_relation":
			err = client.DeleteRelation(ctx, a.RelationID, *note, b.Sources)
		case "retire_entity":
			err = client.Lifecycle(ctx, a.EntityID, "deleted", *note, b.Sources)
		case "restore_entity":
			if e, gerr := client.GetEntity(ctx, a.EntityID); gerr == nil && a.Before != nil {
				_, err = client.UpdateEntity(ctx, a.EntityID, a.Before, e.Version, *note, b.Sources)
			} else if gerr != nil {
				err = gerr
			}
		}
		if err != nil {
			failed++
			fmt.Printf("  失败：%s（%v）\n", a.Reason, err)
		}
	}
	b.RolledBack = time.Now().UTC().Format(time.RFC3339)
	b.Comment("mf-importer", fmt.Sprintf("执行回退：%d 步，失败 %d 步", len(plan), failed))
	if err := store.Save(b); err != nil {
		return err
	}
	if failed > 0 {
		return fmt.Errorf("回退有 %d 步失败，账本已记录，请按失败项手工处理", failed)
	}
	fmt.Println("回退完成。")
	return nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envFloat(key string, fallback float64) float64 {
	if v := os.Getenv(key); v != "" {
		var f float64
		if _, err := fmt.Sscanf(v, "%g", &f); err == nil {
			return f
		}
	}
	return fallback
}
