package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"aruing/internal/core"
	"aruing/internal/tools"
)

// parseAPIResources：4 列默认输出与 5 列（含 APIVERSION）均正确解析；超 maxKeep 截断且不挑类别
func TestParseAPIResources(t *testing.T) {
	// 4 列：NAME SHORTNAMES NAMESPACED KIND，无 APIVERSION
	fourCol := "NAME                              SHORTNAMES   NAMESPACED   KIND\n" +
		"pods                              po           true         Pod\n" +
		"services                          svc          true         Service\n" +
		"ingressroutes                     ico          true         IngressRoute\n" +
		"nodes                                          false        Node\n"
	got := parseAPIResources(fourCol)
	if len(got) != 4 {
		t.Fatalf("4-col len = %d, want 4: %+v", len(got), got)
	}
	// IngressRoute（beta4 动机的 CRD）应被发现，apiGroup 为空（无 APIVERSION 列）
	ir := findResource(got, "IngressRoute")
	if ir == nil {
		t.Fatalf("IngressRoute not parsed: %+v", got)
	}
	if !ir.Namespaced {
		t.Errorf("IngressRoute namespaced = false, want true")
	}
	if ir.APIGroup != "" {
		t.Errorf("4-col IngressRoute apiGroup = %q, want empty", ir.APIGroup)
	}
	// 集群级资源 nodes 应标记为非命名空间级
	if node := findResource(got, "Node"); node == nil || node.Namespaced {
		t.Errorf("Node namespaced misparsed: %+v", node)
	}

	// 5 列：CRD 行带 group/version，核心资源行 APIVERSION 为空（被 Fields 收敛为 4 列）
	fiveCol := "NAME              SHORTNAMES   NAMESPACED   KIND            APIVERSION\n" +
		"pods              po           true         Pod\n" +
		"ingressroutes     ico          true         IngressRoute    traefik.io/v1\n"
	got5 := parseAPIResources(fiveCol)
	if ir := findResource(got5, "IngressRoute"); ir == nil || ir.APIGroup != "traefik.io" {
		t.Errorf("5-col IngressRoute apiGroup = %q, want traefik.io: %+v", ir.APIGroup, ir)
	}
	if pod := findResource(got5, "Pod"); pod == nil || pod.APIGroup != "" {
		t.Errorf("5-col Pod apiGroup = %q, want empty: %+v", pod.APIGroup, pod)
	}

	// 超 maxKeep（300）截断；构造 305 行确认上限生效且不挑类别
	var b strings.Builder
	b.WriteString("NAME   SHORTNAMES   NAMESPACED   KIND\n")
	for i := range 305 {
		fmt.Fprintf(&b, "r%d   x   true   Kind%d\n", i, i)
	}
	if got := parseAPIResources(b.String()); len(got) != 300 {
		t.Errorf("truncate len = %d, want 300", len(got))
	}
}

func findResource(rs []ClusterResource, kind string) *ClusterResource {
	for i := range rs {
		if rs[i].Kind == kind {
			return &rs[i]
		}
	}
	return nil
}

// 模拟 k8s 工具：按配置返回 api-resources stdout 或失败，用于侦察路径测试
type fakeK8sAPIResourcesTool struct {
	stdout string
	fail   bool
}

func (t *fakeK8sAPIResourcesTool) Spec() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        "k8s",
		Description: "fake k8s for recon test",
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}
}

func (t *fakeK8sAPIResourcesTool) Execute(context.Context, json.RawMessage) (*core.Evidence, error) {
	if t.fail {
		return nil, errors.New("simulated api-resources failure")
	}
	raw, _ := json.Marshal(map[string]any{
		"argv":     []string{"api-resources"},
		"exitCode": 0,
		"stdout":   t.stdout,
	})
	return &core.Evidence{
		Source:      "kubernetes",
		ToolName:    "k8s",
		CommandView: "kubectl api-resources",
		Summary:     "kubectl 执行完成，exitCode=0",
		Raw:         raw,
	}, nil
}

// 白盒侦察测试用：可注入字段的最小 factory
type reconTestFactory struct {
	ids []string
	now time.Time
	i   int
}

func (f *reconTestFactory) NewID(prefix string) (string, error) {
	if f.i < len(f.ids) {
		id := f.ids[f.i]
		f.i++
		return id, nil
	}
	f.i++
	return fmt.Sprintf("%s_%d", prefix, f.i), nil
}

func (f *reconTestFactory) Now() time.Time {
	if f.now.IsZero() {
		return time.Now().UTC()
	}
	return f.now
}

// 侦察成功：走 executeTask，返回带发现摘要的 Evidence + 精简清单（含 CRD）
func TestReconClusterSuccess(t *testing.T) {
	registry := tools.NewRegistry()
	if err := registry.Register(&fakeK8sAPIResourcesTool{
		stdout: "NAME           SHORTNAMES   NAMESPACED   KIND\n" +
			"pods           po           true         Pod\n" +
			"ingressroutes  ico          true         IngressRoute\n",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	orch := &Orchestrator{
		executor:     tools.NewDispatcher(registry, tools.NewReadonlyPolicy()),
		factory:      &reconTestFactory{ids: []string{"t_recon", "e_recon"}, now: time.Now().UTC()},
		reconEnabled: true,
	}

	evidence, resources := orch.reconCluster(context.Background(), "run_recon")
	if evidence == nil {
		t.Fatal("evidence = nil, want recon evidence")
	}
	if len(resources) != 2 {
		t.Fatalf("resources = %d, want 2: %+v", len(resources), resources)
	}
	if findResource(resources, "IngressRoute") == nil {
		t.Errorf("resources missing IngressRoute: %+v", resources)
	}
	// Summary 被覆写为侦察成果摘要
	if !strings.Contains(evidence.Summary, "发现") || !strings.Contains(evidence.Summary, "2") {
		t.Errorf("summary = %q, want recon digest", evidence.Summary)
	}
	// 编号经 Factory 发放，Evidence ID 来自 Factory（e_recon）
	if evidence.ID != "e_recon" {
		t.Errorf("evidence ID = %q, want e_recon (factory-issued)", evidence.ID)
	}
	// 原始 stdout 仍在 Raw，供用户深挖
	if !strings.Contains(extractStdout(evidence.Raw), "ingressroutes") {
		t.Errorf("raw stdout lost: %s", evidence.Raw)
	}
}

// 侦察工具失败：error evidence 进链（透明），resources 为 nil，不报错
func TestReconClusterFailure(t *testing.T) {
	registry := tools.NewRegistry()
	if err := registry.Register(&fakeK8sAPIResourcesTool{fail: true}); err != nil {
		t.Fatalf("register: %v", err)
	}
	orch := &Orchestrator{
		executor:     tools.NewDispatcher(registry, tools.NewReadonlyPolicy()),
		factory:      &reconTestFactory{ids: []string{"t_recon", "e_recon"}, now: time.Now().UTC()},
		reconEnabled: true,
	}

	evidence, resources := orch.reconCluster(context.Background(), "run_recon")
	if evidence == nil {
		t.Fatal("evidence = nil, want error evidence (transparent failure)")
	}
	if resources != nil {
		t.Errorf("resources = %+v, want nil on failure", resources)
	}
	if evidence.Error == "" {
		t.Errorf("error evidence should carry Error field: %+v", evidence)
	}
	if !strings.Contains(evidence.Summary, "侦察") || !strings.Contains(evidence.Summary, "失败") {
		t.Errorf("summary = %q, want recon failure note", evidence.Summary)
	}
}

// 侦察未启用（无集群环境）：不尝试、不进证据链，返回 (nil, nil)
// reconEnabled 默认 false，wiring 仅在 k8s 注册后开启，避免 fake/CI 产生噪音
func TestReconClusterDisabled(t *testing.T) {
	registry := tools.NewRegistry()
	if err := registry.Register(&fakeK8sAPIResourcesTool{stdout: "NAME   x   true   Pod\n"}); err != nil {
		t.Fatalf("register: %v", err)
	}
	orch := &Orchestrator{
		executor: tools.NewDispatcher(registry, tools.NewReadonlyPolicy()),
		factory:  &reconTestFactory{now: time.Now().UTC()},
		// reconEnabled 保持默认 false
	}
	evidence, resources := orch.reconCluster(context.Background(), "run_x")
	if evidence != nil || resources != nil {
		t.Errorf("got evidence=%v resources=%v, want nil/nil when recon disabled", evidence, resources)
	}
}
