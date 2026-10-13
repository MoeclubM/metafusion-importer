// mf-importer 曾是 MetaFusion 导入器插件 CLI。
//
// 写目录路径已删除（第 ⑤ 步）：外部来源只允许只读查询（MetaFusion
// POST /api/external-sources/query + MCP external_source_query），不得再自动导入/合并。
//
// 保留的离线子命令：plan（裁剪来源图）、batches/show/comment（本地账本只读/注释）。
// run 与 rollback -apply 已移除。
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/MoeclubM/metafusion-importer/internal/batch"
	"github.com/MoeclubM/metafusion-importer/internal/graph"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "plan":
		err = cmdPlan(os.Args[2:])
	case "run", "rollback":
		fmt.Fprintln(os.Stderr, "错误：写目录路径已删除。请改用 MetaFusion POST /api/external-sources/query（或 MCP external_source_query）只读查询外部来源，再经正常 commit 工作流由人工/Agent 写入。")
		os.Exit(2)
	case "batches":
		err = cmdBatches(os.Args[2:])
	case "show":
		err = cmdShow(os.Args[2:])
	case "comment":
		err = cmdComment(os.Args[2:])
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
		"  batches   [-log 目录]",
		"  show      -log 目录 -id <批次id>",
		"  comment   -log 目录 -id <批次id> -author <人> -body <内容>",
		"",
		"写目录子命令 run / rollback 已删除。外部来源请走 MetaFusion 只读查询 API。",
		"环境变量：MF_IMPORT_LOG_DIR（批次账本目录，默认 ./import-logs）",
	}, "\n") + "\n")
}

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
		return nil, err
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
	in := fs.String("in", "", "来源图 JSON")
	root := fs.String("root", "", "根来源 id")
	depth := fs.Int("depth", 1, "层级")
	_ = fs.Parse(args)
	if *in == "" || *root == "" {
		return fmt.Errorf("需要 -in 与 -root")
	}
	g, err := loadGraph(*in)
	if err != nil {
		return err
	}
	p := g.plan(*root, *depth)
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(p)
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func cmdBatches(args []string) error {
	fs := flag.NewFlagSet("batches", flag.ExitOnError)
	logDir := fs.String("log", envOr("MF_IMPORT_LOG_DIR", "import-logs"), "批次账本目录")
	_ = fs.Parse(args)
	st := batch.Store{Dir: *logDir}
	list, err := st.List()
	if err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(list)
}

func cmdShow(args []string) error {
	fs := flag.NewFlagSet("show", flag.ExitOnError)
	logDir := fs.String("log", envOr("MF_IMPORT_LOG_DIR", "import-logs"), "批次账本目录")
	id := fs.String("id", "", "批次 id")
	_ = fs.Parse(args)
	if *id == "" {
		return fmt.Errorf("需要 -id")
	}
	st := batch.Store{Dir: *logDir}
	b, err := st.Load(*id)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(b)
}

func cmdComment(args []string) error {
	fs := flag.NewFlagSet("comment", flag.ExitOnError)
	logDir := fs.String("log", envOr("MF_IMPORT_LOG_DIR", "import-logs"), "批次账本目录")
	id := fs.String("id", "", "批次 id")
	author := fs.String("author", "", "作者")
	body := fs.String("body", "", "内容")
	_ = fs.Parse(args)
	if *id == "" || *author == "" || *body == "" {
		return fmt.Errorf("需要 -id -author -body")
	}
	st := batch.Store{Dir: *logDir}
	b, err := st.Load(*id)
	if err != nil {
		return err
	}
	b.Comment(*author, *body)
	return st.Save(b)
}
