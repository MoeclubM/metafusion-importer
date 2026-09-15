package batch

import (
	"testing"
	"time"
)

// 回退要说清顺序与范围：逆序、只针对成功的操作、更新要带更新前快照。
func TestRollbackPlanIsReverseAndSkipsNonOK(t *testing.T) {
	b := New("import-1", "bangumi", "12", "某作品", "curator", "首次导入", 2, nil)
	b.Add(Op{Kind: OpCreateEntity, EntityID: "e1", Label: "A", Status: "ok"})
	b.Add(Op{Kind: OpCreateRelation, RelationID: "r1", Label: "A-B", Status: "ok"})
	b.Add(Op{Kind: OpUpdateEntity, EntityID: "e2", Before: map[string]any{"Version": 1}, Status: "ok"})
	b.Add(Op{Kind: OpCreateEntity, EntityID: "e3", Status: "failed", Err: "translation_required"})
	b.Add(Op{Kind: OpCreateEntity, Label: "C", Status: "skipped"})

	plan := b.RollbackPlan()
	if len(plan) != 3 {
		t.Fatalf("回退动作应为 3 条（失败/跳过不算），实际 %d：%+v", len(plan), plan)
	}
	want := []string{"restore_entity", "delete_relation", "retire_entity"}
	for i, w := range want {
		if plan[i].Kind != w {
			t.Fatalf("第 %d 条应为 %s，实际 %s", i, w, plan[i].Kind)
		}
	}
	if plan[0].Before["Version"] != 1 {
		t.Fatalf("还原动作要带更新前快照，实际 %+v", plan[0])
	}
}

// 评论与统计：评论要能累积，统计要能区分成功/失败/跳过。
func TestCommentAndSummary(t *testing.T) {
	b := New("import-2", "bangumi", "12", "某作品", "curator", "导入", 2,
		[]Source{{Kind: "url", Citation: "Bangumi 条目 12", URL: "https://bgm.tv/subject/12"}})
	b.Add(Op{Kind: OpCreateEntity, EntityID: "e1", Status: "ok"})
	b.Add(Op{Kind: OpCreateEntity, Status: "failed", Err: "invalid_term"})
	b.Comment("curator", "B 的日期来源存疑，先按放送日录入")
	if len(b.Comments) != 1 || b.Comments[0].Author != "curator" {
		t.Fatalf("评论未记录：%+v", b.Comments)
	}
	got := b.Summary()
	if !contains(got, "成功 1") || !contains(got, "失败 1") {
		t.Fatalf("统计不符：%s", got)
	}
}

// 落盘与读取：一个批次一个文件，列表按创建时间倒序。
func TestStoreRoundTripAndListOrder(t *testing.T) {
	dir := t.TempDir()
	st := Store{Dir: dir}
	older := New("import-old", "bangumi", "1", "旧", "curator", "", 1, nil)
	older.CreatedAt = time.Now().Add(-time.Hour)
	newer := New("import-new", "bangumi", "2", "新", "curator", "", 1, nil)
	newer.Add(Op{Kind: OpCreateEntity, EntityID: "e9", Status: "ok"})
	for _, b := range []*Batch{older, newer} {
		if err := st.Save(b); err != nil {
			t.Fatal(err)
		}
	}
	list, err := st.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("应有 2 个批次，实际 %d", len(list))
	}
	if list[0].ID != "import-new" {
		t.Fatalf("应按创建时间倒序，实际 %s 在前", list[0].ID)
	}
	back, err := st.Load("import-new")
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Ops) != 1 || back.Ops[0].EntityID != "e9" {
		t.Fatalf("读回的账本不一致：%+v", back.Ops)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (len(sub) == 0 || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
