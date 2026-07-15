package core

import (
	"encoding/json"
	"testing"
	"time"
)

// 已确认目标必须保留开放类型、身份属性和证据来源，避免后续领域扩展修改核心结构
func TestTargetJSON(t *testing.T) {
	want := Target{
		ID:     "target_test",
		RunID:  "run_test",
		NodeID: "node_demo",
		Type:   "future.resource",
		Attrs: map[string]string{
			"future.kind":      "CustomWorkload",
			"future.namespace": "helloworld",
			"future.name":      "demo",
		},
		EvidenceIDs: []string{"evidence_lookup"},
		CreatedAt:   time.Date(2026, 7, 13, 11, 0, 0, 0, time.UTC),
	}

	data, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal target: %v", err)
	}

	var got Target
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal target: %v", err)
	}

	if got.NodeID != want.NodeID || got.Type != want.Type {
		t.Errorf("target source or type was not preserved: %#v", got)
	}
	if got.Attrs["future.kind"] != "CustomWorkload" || got.Attrs["future.namespace"] != "helloworld" {
		t.Errorf("target identity was not preserved: %#v", got.Attrs)
	}
	if len(got.EvidenceIDs) != 1 || got.EvidenceIDs[0] != "evidence_lookup" {
		t.Errorf("target evidence was not preserved: %#v", got.EvidenceIDs)
	}
}
