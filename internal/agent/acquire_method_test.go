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
		{"未知方法报错", "b2-random", 0, true},
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
