package tools

import (
	"encoding/json"
	"testing"
)

// exec 应被诊断策略放行（不论容器内跑什么二进制）
func TestDiagnosticPolicyExecAllowed(t *testing.T) {
	t.Parallel()
	policy := NewDiagnosticPolicy()
	cases := [][]string{
		{"exec", "some-pod", "--", "curl", "localhost:8080/health"},
		{"exec", "some-pod", "-n", "default", "--", "nslookup", "svc-x"},
		{"exec", "some-pod", "--", "rm", "-rf", "/"},
	}
	for _, argv := range cases {
		decision, reason := policy.Check("k8s", mustDiagArgs(t, argv))
		if decision != DecisionAllow {
			t.Errorf("argv=%v decision=%v reason=%q, want allow", argv, decision, reason)
		}
	}
}

// 非 exec 的读类命令仍走 readonly 放行
func TestDiagnosticPolicyReadonlyStillAllowed(t *testing.T) {
	t.Parallel()
	policy := NewDiagnosticPolicy()
	decision, _ := policy.Check("k8s", mustDiagArgs(t, []string{"get", "pods", "-n", "default"}))
	if decision != DecisionAllow {
		t.Fatal("readonly get should still be allowed")
	}
}

// 非 exec 的写类命令仍被 readonly 拒绝（exec 放行不波及 apply/delete）
func TestDiagnosticPolicyWriteStillDenied(t *testing.T) {
	t.Parallel()
	policy := NewDiagnosticPolicy()
	for _, argv := range [][]string{
		{"apply", "-f", "x.yaml"},
		{"delete", "pod", "x"},
	} {
		decision, _ := policy.Check("k8s", mustDiagArgs(t, argv))
		if decision != DecisionDeny {
			t.Errorf("argv=%v decision=%v, want deny", argv, decision)
		}
	}
}

// 非 k8s 工具（fake.* 等）按 readonly 既有宽松放行
func TestDiagnosticPolicyNonK8sAllowed(t *testing.T) {
	t.Parallel()
	policy := NewDiagnosticPolicy()
	decision, _ := policy.Check("fake.list_pods", json.RawMessage(`{}`))
	if decision != DecisionAllow {
		t.Fatalf("non-k8s decision=%v, want allow", decision)
	}
}

// 损坏的 k8s 参数仍由 readonly 兜底拒绝，exec 分支只在可解析时生效
func TestDiagnosticPolicyMalformedArgs(t *testing.T) {
	t.Parallel()
	policy := NewDiagnosticPolicy()
	decision, _ := policy.Check("k8s", json.RawMessage(`not json`))
	if decision != DecisionDeny {
		t.Errorf("malformed args decision=%v, want deny", decision)
	}
}

func mustDiagArgs(t *testing.T, argv []string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"argv": argv})
	if err != nil {
		t.Fatalf("marshal argv: %v", err)
	}
	return raw
}
