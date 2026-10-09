package acquire_test

// Belief 的 JSON 编解码：对数域精确往返（挂起快照跨进程恢复的载荷地基）
// 与非法载荷拒绝（空 / 非有限值——JSON 字面 1e999 会解析成 +Inf）。

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Aruing/Aruing/internal/agent/acquire"
)

// 更新若干轮后导出再导入，对数域状态逐位一致（logp 与 logMass）
func TestBeliefJSONRoundtrip(t *testing.T) {
	b, err := acquire.NewBelief([]float64{0.6, 0.3, 0.1})
	if err != nil {
		t.Fatalf("new belief: %v", err)
	}
	act, err := acquire.NewAction("a", []string{"yes", "no"}, [][]float64{{0.7, 0.3}, {0.2, 0.8}, {0.1, 0.9}}, 1)
	if err != nil {
		t.Fatalf("new action: %v", err)
	}
	// 若干次更新后再导出：让 logp / logMass 都非平凡
	opts := acquire.Options{}
	for range 3 {
		b, _, err = opts.UpdateOutcome(b, act, "yes")
		if err != nil {
			t.Fatalf("update: %v", err)
		}
	}
	data, err := json.Marshal(b)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var restored acquire.Belief
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	a, _ := json.Marshal(restored)
	if string(a) != string(data) {
		t.Fatalf("roundtrip mismatch:\n%s\n%s", data, a)
	}
}

// 非法载荷拒绝：空 logp、非有限值（1e999 → +Inf）、坏 JSON
func TestBeliefJSONRejectsInvalid(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		wantErr string
	}{
		{"empty logp", `{"logp":[],"logMass":0}`, "empty logp"},
		// Go 1.22+ encoding/json 对越界数字直接拒收（1e999 → +Inf 进不了载荷）；
		// 非有限值防线保留作纵深（未来非 encoding/json 路径手构时兕底）
		{"inf logp", `{"logp":[1e999,0],"logMass":0}`, "cannot unmarshal"},
		{"inf logMass", `{"logp":[-1,2],"logMass":1e999}`, "cannot unmarshal"},
		{"corrupt", `{not json`, "invalid character"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var b acquire.Belief
			err := json.Unmarshal([]byte(tc.payload), &b)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
			}
		})
	}
}
