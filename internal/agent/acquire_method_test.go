package agent

import "testing"

// 方法名解析：空/ours → Ours（产品默认）；b1-serial → B1；未知报错
func TestParseAcquireMethod(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    AcquireMethod
		wantErr bool
	}{
		{"空为产品默认 ours", "", AcquireMethodOurs, false},
		{"ours", "ours", AcquireMethodOurs, false},
		{"ours 容忍空白与大小写", "  Ours ", AcquireMethodOurs, false},
		{"b1-serial", "b1-serial", AcquireMethodB1Serial, false},
		{"b2-random 实验臂", "b2-random", AcquireMethodB2Random, false},
		{"b4-cheapest 实验臂", "b4-cheapest", AcquireMethodB4Cheapest, false},
		{"未知方法报错", "b3-react", 0, true},
	}
	for _, tc := range cases {
		got, err := ParseAcquireMethod(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("%s: expected error", tc.name)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("%s: got %v err %v, want %v", tc.name, got, err, tc.want)
		}
	}
}

// 方法名展示：实验臂与产品路径各自规范名（评测记录与进度日志用）
func TestAcquireMethodString(t *testing.T) {
	for m, want := range map[AcquireMethod]string{
		AcquireMethodOurs:       "ours",
		AcquireMethodB1Serial:   "b1-serial",
		AcquireMethodB2Random:   "b2-random",
		AcquireMethodB4Cheapest: "b4-cheapest",
	} {
		if got := m.String(); got != want {
			t.Errorf("String() = %q, want %q", got, want)
		}
	}
}
