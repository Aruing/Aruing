package agent

import (
	"strings"
	"testing"
)

// 教学契约锚：tower prompt 必须教分片全覆盖摘要（map-reduce 投影产物）的读法——
// 片节报数是存在性信号、片头区间可直接作 evidence.read 参数，
// 防止教学文案被无意删除后模型面对分片摘要不知如何跟进（0.1.4 map-reduce 步骤 2）
func TestTowerPromptShardTeaching(t *testing.T) {
	for _, key := range []string{"分片全覆盖", "存在性信号", "片 i/S"} {
		if !strings.Contains(towerPromptTemplate, key) {
			t.Errorf("tower prompt missing teaching keyword %q", key)
		}
	}
}
