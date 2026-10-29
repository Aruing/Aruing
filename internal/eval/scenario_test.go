// 场景难度与规格读取的测试
//
// 六维派生是「理论值」的唯一口径，边界值写死在用例里：改派生规则必须同步改这里，
// 否则难度表会静默漂移
package eval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 六维派生：边界分档与总分求和
func TestDeriveDifficulty(t *testing.T) {
	cases := []struct {
		name string
		in   Difficulty
		want DifficultyScore
	}{
		{
			name: "最小场景：单故障单轮",
			in: Difficulty{
				Faults: 1, EvidenceSources: 1, OutputScale: OutputScalePlain,
				Layers: 1, SessionRounds: 1,
			},
			want: DifficultyScore{
				Faults: 1, EvidenceChain: 1, Environment: 0,
				Layers: 1, SessionLength: 1, Total: 4,
			},
		},
		{
			name: "满格场景：各维触顶",
			in: Difficulty{
				Faults: 3, EvidenceSources: 3, Decoys: 3,
				OutputScale: OutputScaleLarge, CapabilityRestricted: true,
				Layers: 3, SessionRounds: 4,
			},
			want: DifficultyScore{
				Faults: 3, EvidenceChain: 3, Decoys: 3, Environment: 3,
				Layers: 3, SessionLength: 3, Total: 18,
			},
		},
		{
			name: "超上限的计数按 3 分封顶",
			in: Difficulty{
				Faults: 9, EvidenceSources: 9, Decoys: 9,
				OutputScale: OutputScaleLarge, Layers: 9, SessionRounds: 9,
			},
			want: DifficultyScore{
				Faults: 3, EvidenceChain: 3, Decoys: 3, Environment: 2,
				Layers: 3, SessionLength: 3, Total: 17,
			},
		},
		{
			name: "三轮仍在短会话档",
			in: Difficulty{
				Faults: 1, EvidenceSources: 1, OutputScale: OutputScalePlain,
				Layers: 1, SessionRounds: 3,
			},
			want: DifficultyScore{
				Faults: 1, EvidenceChain: 1, Layers: 1,
				SessionLength: 2, Total: 5,
			},
		},
		{
			name: "未标注的零值折出全零，不默认任何分",
			in:   Difficulty{},
			want: DifficultyScore{},
		},
		{
			name: "仅能力受限的环境",
			in: Difficulty{
				Faults: 1, EvidenceSources: 1, OutputScale: OutputScalePlain,
				CapabilityRestricted: true, Layers: 1, SessionRounds: 1,
			},
			want: DifficultyScore{
				Faults: 1, EvidenceChain: 1, Environment: 1,
				Layers: 1, SessionLength: 1, Total: 5,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DeriveDifficulty(tc.in)
			if got != tc.want {
				t.Fatalf("DeriveDifficulty(%+v) = %+v, want %+v", tc.in, got, tc.want)
			}
		})
	}
}

// 严格读取：有标注才可判分，缺段与越界都要明确报错
func TestLoadScenarioSpec(t *testing.T) {
	cases := []struct {
		name    string
		content string
		wantErr string
	}{
		{
			name: "正常标注",
			content: `
name: demo
ground_truth:
  resource_type: pod
  resource_name: demo-api
  namespace: demo
  fault_type: bad-image
difficulty:
  faults: 2
  evidence_sources: 3
  decoys: 1
  output_scale: large
  capability_restricted: true
  layers: 2
  session_rounds: 4
  info_gap: true
`,
		},
		{
			name: "缺难度段",
			content: `
name: demo
ground_truth:
  resource_name: demo-api
`,
			wantErr: "difficulty is required",
		},
		{
			name: "体量档位非法",
			content: `
name: demo
ground_truth:
  resource_name: demo-api
difficulty:
  faults: 1
  output_scale: huge
  session_rounds: 1
`,
			wantErr: "output_scale",
		},
		{
			name: "诱饵数为负",
			content: `
name: demo
ground_truth:
  resource_name: demo-api
difficulty:
  faults: 1
  decoys: -1
  output_scale: plain
  session_rounds: 1
`,
			wantErr: "decoys must not be negative",
		},
		{
			name: "没有真故障",
			content: `
name: demo
ground_truth:
  resource_name: demo-api
difficulty:
  faults: 0
  output_scale: plain
  session_rounds: 1
`,
			wantErr: "faults must be at least 1",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeTemp(t, "scenario.yaml", tc.content)
			spec, err := LoadScenarioSpec(path)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("LoadScenarioSpec: %v", err)
			}
			if spec.Difficulty == nil {
				t.Fatal("difficulty = nil, want parsed")
			}
			if !spec.Difficulty.InfoGap {
				t.Fatal("info_gap = false, want true")
			}
		})
	}
}

// 场景发现：按 manifests/ 判定场景、隐藏目录跳过、缺标注与坏标注分开处理
func TestLoadScenarioSpecs(t *testing.T) {
	root := t.TempDir()
	writeScenarioDir(t, root, "beta", `
name: beta
ground_truth:
  resource_name: beta-api
`)
	writeScenarioDir(t, root, "alpha", `
name: alpha
ground_truth:
  resource_name: alpha-api
difficulty:
  faults: 1
  evidence_sources: 1
  output_scale: plain
  layers: 1
  session_rounds: 1
  info_gap: true
`)
	writeScenarioDir(t, root, "gamma", `
name: gamma
ground_truth:
  resource_name: gamma-api
difficulty:
  faults: 1
  output_scale: plain
  session_rounds: 1
  layers: nope
`)
	// 无 manifests/ 的目录不是场景
	if err := os.MkdirAll(filepath.Join(root, "not-a-scenario"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// 隐藏目录是运行期产物（.kube / .data），不进列表
	if err := os.MkdirAll(filepath.Join(root, ".kube", "manifests"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	entries, err := LoadScenarioSpecs(root)
	if err != nil {
		t.Fatalf("LoadScenarioSpecs: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("entries = %d, want 3", len(entries))
	}

	// 目录名字典序：alpha / beta / gamma
	if entries[0].Name != "alpha" || entries[1].Name != "beta" || entries[2].Name != "gamma" {
		t.Fatalf("order = %s/%s/%s", entries[0].Name, entries[1].Name, entries[2].Name)
	}
	if entries[0].Err != nil || entries[0].Spec.Difficulty == nil {
		t.Fatalf("alpha: err = %v, difficulty = %v", entries[0].Err, entries[0].Spec.Difficulty)
	}
	// 未标注难度不算错误：展示层显示占位
	if entries[1].Err != nil {
		t.Fatalf("beta err = %v, want nil", entries[1].Err)
	}
	if entries[1].Spec.Difficulty != nil {
		t.Fatalf("beta difficulty = %+v, want nil", entries[1].Spec.Difficulty)
	}
	// 标注越界是数据错误，必须暴露给调用方
	if entries[2].Err == nil {
		t.Fatal("gamma err = nil, want parse error")
	}
}

// 全库回填守卫：每个含 manifests/ 的场景都必须带合法的 difficulty 段
// 新增场景漏标注时本用例直接失败，回填债不能靠人记得
func TestScenariosAnnotated(t *testing.T) {
	root := filepath.Join("..", "..", "scenarios")
	entries, err := LoadScenarioSpecs(root)
	if err != nil {
		t.Skipf("scenarios dir absent: %v", err)
	}
	if len(entries) == 0 {
		t.Skip("no scenarios found")
	}

	for _, e := range entries {
		t.Run(e.Name, func(t *testing.T) {
			// 严格口径同时校验缺段与越界：展示侧的宽松口径会默默显示占位
			spec, err := LoadScenarioSpec(e.Path)
			if err != nil {
				t.Fatalf("LoadScenarioSpec: %v", err)
			}
			if spec.Difficulty == nil {
				t.Fatal("difficulty = nil, want annotated")
			}
		})
	}
}

// 难度表快照：总分与信息缺口标记一旦被误改（断崖、抄错行）立刻暴露
// 只钉派生结果不钉原子事实，避免把 yaml 内容复制成第二份事实源
func TestScenariosDifficultyTable(t *testing.T) {
	root := filepath.Join("..", "..", "scenarios")
	cases := []struct {
		name  string
		total int
		gap   bool
	}{
		{name: "bigtable-fleet", total: 10},
		{name: "configmap-missing", total: 6},
		{name: "crashloop-bad-image", total: 5},
		{name: "flapping-crashloop", total: 6},
		{name: "log-time-window", total: 5},
		{name: "netpol-misfire", total: 10, gap: true},
		{name: "oom-killed", total: 5},
		{name: "pending-nodeselector", total: 7},
		{name: "quota-vs-scheduler", total: 9},
		{name: "rollout-stuck", total: 9},
		{name: "same-name-multi-ns", total: 8, gap: true},
		{name: "shared-config-drift", total: 10},
		{name: "svc-wrong-selector", total: 5},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(root, tc.name, "scenario.yaml")
			if _, err := os.Stat(path); err != nil {
				t.Skipf("scenario file absent: %v", err)
			}
			spec, err := LoadScenarioSpec(path)
			if err != nil {
				t.Fatalf("LoadScenarioSpec: %v", err)
			}
			score := DeriveDifficulty(*spec.Difficulty)
			if score.Total != tc.total {
				t.Fatalf("total = %d, want %d", score.Total, tc.total)
			}
			if spec.Difficulty.InfoGap != tc.gap {
				t.Fatalf("info_gap = %v, want %v", spec.Difficulty.InfoGap, tc.gap)
			}
		})
	}
}

// 落一个带 manifests/ 的场景目录
func writeScenarioDir(t *testing.T, root, name, manifest string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Join(dir, "manifests"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "scenario.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write scenario: %v", err)
	}
}
