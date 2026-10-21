package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/Aruing/Aruing/internal/session"
)

// 测试用会话汇总：固定时间便于断言展示正文
func testSummaries() []session.SessionSummary {
	at := time.Date(2026, 10, 21, 15, 4, 5, 0, time.UTC)
	return []session.SessionSummary{
		{ID: "sess_b", MessageCount: 3, UpdatedAt: at, FirstQuestion: "为什么 demo-api 起不来"},
		{ID: "sess_a", MessageCount: 1, UpdatedAt: at, FirstQuestion: "查 redis"},
	}
}

// 条目拼装：当前会话带 * 标记，顺序沿用列表返回的最近活跃降序
func TestSessionItems(t *testing.T) {
	items := sessionItems(testSummaries(), "sess_a")
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2", len(items))
	}
	if items[0].id != "sess_b" || items[0].cur {
		t.Fatalf("items[0] = %+v (order must follow lister, non-current)", items[0])
	}
	if items[1].id != "sess_a" || !items[1].cur {
		t.Fatalf("items[1] = %+v (current session must be marked)", items[1])
	}
	if !strings.HasPrefix(items[1].label, "* sess_a") {
		t.Fatalf("current label = %q, want * prefix", items[1].label)
	}
	if !strings.Contains(items[0].label, "3 条消息") || !strings.Contains(items[0].label, "为什么 demo-api 起不来") {
		t.Fatalf("label missing fields: %q", items[0].label)
	}
}

// 过滤：空 query 全量；按 id 与首问预览都能命中；大小写不敏感；无命中为空
func TestFilterSessions(t *testing.T) {
	items := sessionItems(testSummaries(), "sess_a")
	cases := []struct {
		name  string
		query string
		want  []string
	}{
		{"空查询全量", "", []string{"sess_b", "sess_a"}},
		{"空白查询视为空", "   ", []string{"sess_b", "sess_a"}},
		{"按 id 前缀", "sess_b", []string{"sess_b"}},
		{"按首问关键词", "redis", []string{"sess_a"}},
		{"大小写不敏感", "REDIS", []string{"sess_a"}},
		{"无命中", "zzz", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := filterSessions(items, c.query)
			if len(got) != len(c.want) {
				t.Fatalf("filtered = %d, want %d (%+v)", len(got), len(c.want), got)
			}
			for i, want := range c.want {
				if got[i].id != want {
					t.Fatalf("filtered[%d] = %s, want %s", i, got[i].id, want)
				}
			}
		})
	}
}

// 选中移动钳制在 [0, len)；Enter 取选中会话；空视图不产出目标
func TestPickerSelect(t *testing.T) {
	v := pickerView{items: sessionItems(testSummaries(), "sess_a")}
	if v = v.moveSel(-1); v.sel != 0 {
		t.Fatalf("sel = %d, want clamp at 0", v.sel)
	}
	if v = v.moveSel(5); v.sel != 1 {
		t.Fatalf("sel = %d, want clamp at 1", v.sel)
	}
	if id, ok := pickerTarget(v); !ok || id != "sess_a" {
		t.Fatalf("pickerTarget = (%q, %v), want (sess_a, true)", id, ok)
	}
	if id, ok := pickerTarget(pickerView{}); ok || id != "" {
		t.Fatalf("empty picker must not yield a target: (%q, %v)", id, ok)
	}
}

// 展示行：选中行带 ❯ 前缀、未选中对齐，末行为键位自述（完成标志 2 可发现性）
func TestPickerRows(t *testing.T) {
	v := pickerView{items: sessionItems(testSummaries(), "sess_a")}
	rows := pickerRows(v)
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3 (2 sessions + key hint)", len(rows))
	}
	if !strings.HasPrefix(rows[0].text, "❯ ") || !rows[0].selected {
		t.Fatalf("selected row = %+v", rows[0])
	}
	if !strings.HasPrefix(rows[1].text, "  ") || rows[1].selected {
		t.Fatalf("unselected row = %+v", rows[1])
	}
	if last := rows[len(rows)-1]; last.text != pickerKeyHint {
		t.Fatalf("last row = %q, want key hint %q", last.text, pickerKeyHint)
	}
}
