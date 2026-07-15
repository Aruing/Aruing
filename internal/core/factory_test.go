package core

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"
)

// 实体编号必须携带开放前缀和可排序时间，避免存储层再维护第二套身份规则
func TestFactoryID(t *testing.T) {
	now := time.Date(2026, 7, 15, 10, 30, 0, 123_000_000, time.FixedZone("CST", 8*60*60))
	factory := newFactory(func() time.Time { return now }, bytes.NewReader(bytes.Repeat([]byte{0xab}, 10)))

	id, err := factory.NewID("run")
	if err != nil {
		t.Fatalf("new ID: %v", err)
	}
	if !strings.HasPrefix(id, "run_") {
		t.Fatalf("ID = %q, want run_ prefix", id)
	}

	uuid := strings.TrimPrefix(id, "run_")
	if len(uuid) != 36 || uuid[8] != '-' || uuid[13] != '-' || uuid[18] != '-' || uuid[23] != '-' {
		t.Fatalf("UUID = %q, want canonical format", uuid)
	}
	raw := strings.ReplaceAll(uuid, "-", "")
	if raw[:12] != fmt.Sprintf("%012x", now.UnixMilli()) {
		t.Errorf("UUID timestamp = %q, want %012x", raw[:12], now.UnixMilli())
	}
	if uuid[14] != '7' {
		t.Errorf("UUID version = %q, want 7", uuid[14])
	}
	if !strings.ContainsRune("89ab", rune(uuid[19])) {
		t.Errorf("UUID variant = %q, want RFC 9562 variant", uuid[19])
	}
}

// 统一时间必须使用世界协调时，避免不同运行环境产生时区相关存储差异
func TestFactoryNow(t *testing.T) {
	want := time.Date(2026, 7, 15, 2, 30, 0, 0, time.UTC)
	local := want.In(time.FixedZone("CST", 8*60*60))
	factory := newFactory(func() time.Time { return local }, nil)

	got := factory.Now()
	if !got.Equal(want) || got.Location() != time.UTC {
		t.Errorf("Now() = %v in %v, want %v in UTC", got, got.Location(), want)
	}
}

// 缺少前缀或随机数据时必须返回错误，避免生成无法区分或随机性不足的编号
func TestFactoryValidate(t *testing.T) {
	now := func() time.Time { return time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC) }
	tests := []struct {
		name    string
		factory *Factory
		prefix  string
	}{
		{
			name:    "missing prefix",
			factory: newFactory(now, bytes.NewReader(bytes.Repeat([]byte{1}, 10))),
		},
		{
			name:    "missing random data",
			factory: newFactory(now, bytes.NewReader(nil)),
			prefix:  "run",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := test.factory.NewID(test.prefix); err == nil {
				t.Fatal("new ID: error = nil")
			}
		})
	}
}
