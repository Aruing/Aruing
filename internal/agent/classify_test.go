package agent

import "testing"

// 机械归类：唯一命中、大小写归一、零命中、多命中
func TestClassifyOutcome(t *testing.T) {
	cases := []struct {
		name     string
		text     string
		outcomes []string
		want     string
		hit      bool
	}{
		{"唯一命中且大小写归一", "pod CrashLoopBackOff restarts 8", []string{"crash", "running"}, "crash", true},
		{"零命中", "一切正常", []string{"crash", "running"}, "", false},
		{"多命中不可归类", "crash 与 running 并存", []string{"crash", "running"}, "", false},
		{"短标签天然多命中自限", "state u v", []string{"u", "v"}, "", false},
		{"空标签跳过", "crash", []string{"", "crash"}, "crash", true},
	}
	for _, tc := range cases {
		got, hit := classifyOutcome(tc.text, tc.outcomes)
		if hit != tc.hit || got != tc.want {
			t.Errorf("%s: got (%q, %v), want (%q, %v)", tc.name, got, hit, tc.want, tc.hit)
		}
	}
}
