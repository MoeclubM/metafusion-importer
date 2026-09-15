// Package graph 把"按层级抓取"这件事单独拿出来算清楚：抓什么、抓到哪一层、
// 哪些节点只建基础信息、哪些关系才允许写。
//
// 规则（来自产品要求，逐字对应）：
//
//	层级 2、添加主实体 A，存在链条 A-B-C 时：
//	  完整添加 A-B（实体 + A-B 之间的关系）；
//	  C 只添加基础信息，**不建立与缺失项目的关系**。
//
// 因此判据是"节点到根的跳数"：
//
//	depth < MaxDepth  -> Full：建实体，并与同样 Full 的节点建关系；
//	depth == MaxDepth -> Basic：只建实体基础信息，不参与任何关系；
//	depth > MaxDepth  -> 直接丢弃（连基础信息都不建）。
//
// 关系只有在两端都是 Full 时才写：一端是 Basic 就说明"那条链的其余部分还没进来"，
// 此时建关系会留下指向缺失项目的悬挂边。
package graph

import "sort"

// Node 是从来源站点看到的一个实体。
type Node struct {
	// SourceID 来源侧标识（如 Bangumi subject 12 / person 45638）。
	SourceID string
	// Kind 期望落在目录里的 kind（work / agent / …），由来源映射决定。
	Kind string
	// Title 抓取到的题名（仅用于预览与日志）。
	Title string
	// Depth 距离根实体的跳数，根为 0。
	Depth int
}

// Edge 是来源侧的一条关系（两端的 SourceID）。
type Edge struct {
	From string
	To   string
	// Type 目录侧关系码（如 sequel_of / character_in），由来源映射决定。
	Type string
}

// Plan 是一次抓取的执行计划。
type Plan struct {
	Root     string
	MaxDepth int
	// Full：完整导入（实体 + 关系）。
	Full []Node
	// Basic：只建基础信息，不建关系。
	Basic []Node
	// Edges：两端都在 Full 里的关系。
	Edges []Edge
	// DroppedEdges：因为一端不在 Full 里而被丢弃的关系（写日志用，便于解释"为什么没连上"）。
	DroppedEdges []Edge
}

// Build 从根节点出发做 BFS，产出执行计划。
// nodes/edges 是抓取阶段已经拿到的来源侧数据；未知端点（edges 指向不在 nodes 里的 id）会被忽略，
// 因为"缺失项目"本来就还没抓进来。
func Build(root string, maxDepth int, nodes []Node, edges []Edge) Plan {
	if maxDepth < 0 {
		maxDepth = 0
	}
	byID := map[string]Node{}
	for _, n := range nodes {
		if n.SourceID == "" {
			continue
		}
		byID[n.SourceID] = n
	}
	// 邻接表（无向）：层级抓取关心"离根几跳"，不关心方向。
	adj := map[string][]string{}
	for _, e := range edges {
		if e.From == "" || e.To == "" {
			continue
		}
		adj[e.From] = append(adj[e.From], e.To)
		adj[e.To] = append(adj[e.To], e.From)
	}

	depth := map[string]int{root: 0}
	queue := []string{root}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, next := range adj[cur] {
			if _, seen := depth[next]; seen {
				continue // 环或已访问：BFS 保证首次到达就是最短跳数
			}
			depth[next] = depth[cur] + 1
			queue = append(queue, next)
		}
	}

	plan := Plan{Root: root, MaxDepth: maxDepth}
	fullSet := map[string]bool{}
	// 根节点按同一判据处理：MaxDepth=0 时根也退化为基础信息（即"只加主实体本身"，
	// 不建任何关系），与 depth < MaxDepth 的规则保持一致。
	for id, d := range depth {
		n, ok := byID[id]
		if !ok {
			continue
		}
		n.Depth = d
		switch {
		case d < maxDepth:
			plan.Full = append(plan.Full, n)
			fullSet[id] = true
		case d == maxDepth:
			plan.Basic = append(plan.Basic, n)
		}
	}
	sortNodes(plan.Full)
	sortNodes(plan.Basic)
	for _, e := range edges {
		if fullSet[e.From] && fullSet[e.To] {
			plan.Edges = append(plan.Edges, e)
			continue
		}
		if _, ok := byID[e.From]; !ok {
			continue // 端点在"缺失项目"里：不是被层级裁掉的，不记录
		}
		if _, ok := byID[e.To]; !ok {
			continue
		}
		plan.DroppedEdges = append(plan.DroppedEdges, e)
	}
	return plan
}

// sortNodes 让输出稳定（按跳数、再按来源 id），便于对比两次抓取的差异。
func sortNodes(ns []Node) {
	sort.Slice(ns, func(i, j int) bool {
		if ns[i].Depth != ns[j].Depth {
			return ns[i].Depth < ns[j].Depth
		}
		return ns[i].SourceID < ns[j].SourceID
	})
}
