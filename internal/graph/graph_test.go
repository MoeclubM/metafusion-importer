package graph

import "testing"

// 产品要求的原始例子：层级 2、A-B-C 链条 —— A-B 完整，C 只建基础信息且不建关系。
func TestDepthTwoKeepsABAndBasicsC(t *testing.T) {
	nodes := []Node{
		{SourceID: "A", Kind: "work", Title: "A", Depth: 0},
		{SourceID: "B", Kind: "work", Title: "B"},
		{SourceID: "C", Kind: "agent", Title: "C"},
	}
	edges := []Edge{{From: "A", To: "B", Type: "sequel_of"}, {From: "B", To: "C", Type: "created_by"}}
	p := Build("A", 2, nodes, edges)

	if len(p.Full) != 2 {
		t.Fatalf("完整导入应为 A、B，实际 %d 个：%+v", len(p.Full), p.Full)
	}
	if len(p.Basic) != 1 || p.Basic[0].SourceID != "C" {
		t.Fatalf("C 应只建基础信息，实际 %+v", p.Basic)
	}
	if len(p.Edges) != 1 || p.Edges[0].Type != "sequel_of" {
		t.Fatalf("只应写 A-B 的关系，实际 %+v", p.Edges)
	}
	if len(p.DroppedEdges) != 1 || p.DroppedEdges[0].To != "C" {
		t.Fatalf("B-C 应被丢弃并记录，实际 %+v", p.DroppedEdges)
	}
}

// 层级 1：只完整导入根，其余全部退化为基础信息。
func TestDepthOneOnlyRootIsFull(t *testing.T) {
	nodes := []Node{{SourceID: "A", Kind: "work"}, {SourceID: "B", Kind: "agent"}}
	p := Build("A", 1, nodes, []Edge{{From: "A", To: "B", Type: "created_by"}})
	if len(p.Full) != 1 || p.Full[0].SourceID != "A" {
		t.Fatalf("层级 1 只应完整导入根，实际 %+v", p.Full)
	}
	if len(p.Basic) != 1 || len(p.Edges) != 0 {
		t.Fatalf("邻居应为基础信息且不建关系，实际 basic=%+v edges=%+v", p.Basic, p.Edges)
	}
}

// 层级 0（只加主实体本身）：根退化为"只建基础信息"，邻居连进都不进（超出层级）。
func TestDepthZero(t *testing.T) {
	nodes := []Node{{SourceID: "A", Kind: "work"}, {SourceID: "B", Kind: "work"}}
	p := Build("A", 0, nodes, []Edge{{From: "A", To: "B", Type: "sequel_of"}})
	if len(p.Full) != 0 {
		t.Fatalf("层级 0 不该有完整导入项，实际 %+v", p.Full)
	}
	if len(p.Basic) != 1 || p.Basic[0].SourceID != "A" {
		t.Fatalf("层级 0 只应建根的基础信息，实际 %+v", p.Basic)
	}
	if len(p.Edges) != 0 {
		t.Fatalf("层级 0 不写任何关系，实际 %+v", p.Edges)
	}
}

// 环与重复边不能让 BFS 打转，也不该让同一个节点出现两次。
func TestCycleIsSafe(t *testing.T) {
	nodes := []Node{{SourceID: "A", Kind: "work"}, {SourceID: "B", Kind: "work"}, {SourceID: "C", Kind: "work"}}
	edges := []Edge{
		{From: "A", To: "B", Type: "sequel_of"},
		{From: "B", To: "C", Type: "sequel_of"},
		{From: "C", To: "A", Type: "sequel_of"},
		{From: "A", To: "B", Type: "sequel_of"},
	}
	p := Build("A", 3, nodes, edges)
	if len(p.Full) != 3 {
		t.Fatalf("三个节点都应完整导入且不重复，实际 %+v", p.Full)
	}
	seen := map[string]int{}
	for _, n := range p.Full {
		seen[n.SourceID]++
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("%s 出现了 %d 次", id, n)
		}
	}
	// 环上的边都会写（两端都 Full），重复边按来源数据原样保留，去重交给目录侧的
	// duplicate_relation 校验与批次账本（见 batch 包）。
	if len(p.Edges) != 4 {
		t.Fatalf("四条边两端都 Full，应全部保留，实际 %d", len(p.Edges))
	}
}

// 根节点自己不在 nodes 里（抓取失败）时不该 panic，也不应产生空的 Full。
func TestUnknownRoot(t *testing.T) {
	p := Build("missing", 2, []Node{{SourceID: "B", Kind: "work"}}, nil)
	if len(p.Full) != 0 {
		t.Fatalf("根抓不到时不应有完整导入项，实际 %+v", p.Full)
	}
}
