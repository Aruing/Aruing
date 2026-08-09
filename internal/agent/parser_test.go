package agent_test

import (
	"context"
	"testing"
	"time"

	"aruing/internal/agent/agenttest"
	"aruing/internal/core"
)

// 假解析器应稳定返回预设的问题结构，并把结果绑定到当前运行
func TestFakeParserParse(t *testing.T) {
	parser := agenttest.NewFakeParser(core.Query{
		ID:    "query_test",
		RunID: "stale_run",
		Goal:  "定位 demo 无法访问的原因",
		Nodes: []core.Node{{
			ID:   "node_demo",
			Type: "resource",
			Text: "demo",
			Attrs: map[string]string{
				"hint.name": "demo",
			},
		}},
		CreatedAt: time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC),
	})

	got, err := parser.Parse(context.Background(), core.Run{
		ID:       "run_test",
		Question: "demo 为什么访问不了",
	})
	if err != nil {
		t.Fatalf("parse query: %v", err)
	}
	if got.ID != "query_test" || got.RunID != "run_test" {
		t.Errorf("query identity was not preserved: %#v", got)
	}
	if len(got.Nodes) != 1 || got.Nodes[0].Attrs["hint.name"] != "demo" {
		t.Errorf("query clues were not preserved: %#v", got.Nodes)
	}

	// 多次运行不能共享可变属性，否则一次解析结果可能污染后续运行
	got.Nodes[0].Attrs["hint.name"] = "changed"
	again, err := parser.Parse(context.Background(), core.Run{
		ID:       "run_again",
		Question: "再检查一次 demo",
	})
	if err != nil {
		t.Fatalf("parse query again: %v", err)
	}
	if again.Nodes[0].Attrs["hint.name"] != "demo" {
		t.Errorf("query template was mutated: %#v", again.Nodes[0].Attrs)
	}
}

// 缺少运行身份、原始问题或预设结构时应尽早失败，避免产生无法关联的问题数据
func TestFakeParserValidate(t *testing.T) {
	tests := []struct {
		name  string
		query core.Query
		run   core.Run
	}{
		{
			name:  "missing run ID",
			query: core.Query{ID: "query_test"},
			run:   core.Run{Question: "demo 为什么访问不了"},
		},
		{
			name:  "missing question",
			query: core.Query{ID: "query_test"},
			run:   core.Run{ID: "run_test"},
		},
		{
			name: "missing query ID",
			run:  core.Run{ID: "run_test", Question: "demo 为什么访问不了"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := agenttest.NewFakeParser(test.query).Parse(context.Background(), test.run)
			if err == nil {
				t.Fatal("parse query: error = nil")
			}
		})
	}
}
