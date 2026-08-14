package k8s

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"aruing/internal/tools"
)

// 用临时脚本伪造集群命令，验证参数透传与证据语义
func TestToolExecuteSuccess(t *testing.T) {
	kubectl := writeFakeKubectl(t, `#!/bin/sh
printf '%s\n' "$@" > "$FAKE_KUBECTL_ARGV"
printf 'stdout-body'
printf 'stderr-body' 1>&2
exit 0
`)
	tool := mustNewTool(t, Config{
		KubectlPath:    kubectl,
		DefaultTimeout: 5 * time.Second,
		MaxTimeout:     10 * time.Second,
		MaxStdoutBytes: 1024,
		MaxStderrBytes: 1024,
	})

	argvFile := filepath.Join(t.TempDir(), "argv.txt")
	t.Setenv("FAKE_KUBECTL_ARGV", argvFile)

	evidence, err := tool.Execute(context.Background(), mustArgs(t, map[string]any{
		"argv": []string{"get", "pods", "-n", "default", "-o", "json"},
	}))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if evidence.Source != evidenceSource {
		t.Errorf("Source = %q, want %q", evidence.Source, evidenceSource)
	}
	if evidence.ToolName != toolName {
		t.Errorf("ToolName = %q, want %q", evidence.ToolName, toolName)
	}
	if evidence.Error != "" {
		t.Errorf("Error = %q, want empty", evidence.Error)
	}
	if !strings.Contains(evidence.CommandView, "kubectl get pods") {
		t.Errorf("CommandView = %q", evidence.CommandView)
	}

	raw := decodeRaw(t, evidence.Raw)
	if raw.ExitCode != 0 {
		t.Errorf("exitCode = %d, want 0", raw.ExitCode)
	}
	if raw.Stdout != "stdout-body" {
		t.Errorf("stdout = %q", raw.Stdout)
	}
	if raw.Stderr != "stderr-body" {
		t.Errorf("stderr = %q", raw.Stderr)
	}
	if got := readFile(t, argvFile); got != "get\npods\n-n\ndefault\n-o\njson\n" {
		t.Fatalf("argv file = %q", got)
	}
}

// get 类表格输出应被投影成结构化摘要（类型/行数/列/行），不再是无用的「执行完成」字符串
func TestToolExecuteProjectsTable(t *testing.T) {
	kubectl := writeFakeKubectl(t, `#!/bin/sh
printf 'NAME   READY   STATUS\nweb    1/1     Running\napi    0/1     CrashLoopBackOff\n'
exit 0
`)
	tool := mustNewTool(t, Config{KubectlPath: kubectl})

	evidence, err := tool.Execute(context.Background(), mustArgs(t, map[string]any{
		"argv": []string{"get", "pods"},
	}))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	for _, want := range []string{"pods · 2 行", "NAME READY STATUS", "CrashLoopBackOff", "web"} {
		if !strings.Contains(evidence.Summary, want) {
			t.Errorf("Summary missing %q, got:\n%s", want, evidence.Summary)
		}
	}
	if strings.Contains(evidence.Summary, "执行完成") {
		t.Errorf("Summary should drop the old placeholder, got:\n%s", evidence.Summary)
	}
}

// 非零退出码应写入证据而不是变成语言错误，便于上层纠错
func TestToolExecuteNonZeroExit(t *testing.T) {
	kubectl := writeFakeKubectl(t, `#!/bin/sh
printf 'boom' 1>&2
exit 3
`)
	tool := mustNewTool(t, Config{KubectlPath: kubectl})

	evidence, err := tool.Execute(context.Background(), mustArgs(t, map[string]any{
		"argv": []string{"get", "ns"},
	}))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if evidence.Error == "" {
		t.Fatal("Error empty, want non-empty")
	}
	raw := decodeRaw(t, evidence.Raw)
	if raw.ExitCode != 3 {
		t.Errorf("exitCode = %d, want 3", raw.ExitCode)
	}
	if raw.Stderr != "boom" {
		t.Errorf("stderr = %q", raw.Stderr)
	}
}

// 标准输入应完整进入子进程，证据只保留长度与哈希
func TestToolExecuteStdin(t *testing.T) {
	kubectl := writeFakeKubectl(t, `#!/bin/sh
cat > "$FAKE_KUBECTL_STDIN"
exit 0
`)
	tool := mustNewTool(t, Config{KubectlPath: kubectl})
	stdinFile := filepath.Join(t.TempDir(), "stdin.txt")
	t.Setenv("FAKE_KUBECTL_STDIN", stdinFile)

	body := "apiVersion: v1\nkind: ConfigMap\n"
	evidence, err := tool.Execute(context.Background(), mustArgs(t, map[string]any{
		"argv":  []string{"apply", "-f", "-"},
		"stdin": body,
	}))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := readFile(t, stdinFile); got != body {
		t.Fatalf("stdin = %q, want %q", got, body)
	}
	raw := decodeRaw(t, evidence.Raw)
	if raw.StdinBytes != len(body) {
		t.Errorf("stdinBytes = %d, want %d", raw.StdinBytes, len(body))
	}
	sum := sha256.Sum256([]byte(body))
	if raw.StdinSHA256 != hex.EncodeToString(sum[:]) {
		t.Errorf("stdinSHA256 = %q", raw.StdinSHA256)
	}
	if strings.Contains(string(evidence.Raw), body) {
		t.Fatal("evidence raw should not contain full stdin")
	}
}

// 超大输出应截断并标记，避免证据无限膨胀
func TestToolExecuteTruncate(t *testing.T) {
	kubectl := writeFakeKubectl(t, `#!/bin/sh
printf 'ABCDEFGHIJ'
printf 'abcdefghij' 1>&2
exit 0
`)
	tool := mustNewTool(t, Config{
		KubectlPath:    kubectl,
		MaxStdoutBytes: 4,
		MaxStderrBytes: 3,
	})

	evidence, err := tool.Execute(context.Background(), mustArgs(t, map[string]any{
		"argv": []string{"version"},
	}))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	raw := decodeRaw(t, evidence.Raw)
	if raw.Stdout != "ABCD" || !raw.StdoutTruncated {
		t.Fatalf("stdout = %q truncated=%v", raw.Stdout, raw.StdoutTruncated)
	}
	if raw.Stderr != "abc" || !raw.StderrTruncated {
		t.Fatalf("stderr = %q truncated=%v", raw.Stderr, raw.StderrTruncated)
	}
	if !strings.Contains(evidence.Summary, "截断") {
		t.Errorf("Summary = %q", evidence.Summary)
	}
}

// 超时应返回错误，不伪装成成功证据
func TestToolExecuteTimeout(t *testing.T) {
	kubectl := writeFakeKubectl(t, `#!/bin/sh
sleep 5
exit 0
`)
	tool := mustNewTool(t, Config{
		KubectlPath:    kubectl,
		DefaultTimeout: 50 * time.Millisecond,
		MaxTimeout:     time.Second,
	})

	_, err := tool.Execute(context.Background(), mustArgs(t, map[string]any{
		"argv": []string{"get", "pods"},
	}))
	if err == nil {
		t.Fatal("error = nil, want timeout")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("error = %q", err)
	}
}

// 无法启动二进制时应返回运行时错误
func TestToolExecuteMissingBinary(t *testing.T) {
	tool := mustNewTool(t, Config{
		KubectlPath: filepath.Join(t.TempDir(), "missing-kubectl"),
	})
	_, err := tool.Execute(context.Background(), mustArgs(t, map[string]any{
		"argv": []string{"get", "pods"},
	}))
	if err == nil {
		t.Fatal("error = nil, want start failure")
	}
}

// 参数必须符合规格，未知字段与空参数列表应被拒绝
func TestToolParseArgs(t *testing.T) {
	tool := mustNewTool(t, Config{KubectlPath: writeFakeKubectl(t, "#!/bin/sh\nexit 0\n")})

	tests := []struct {
		name string
		args string
		want string
	}{
		{name: "empty", args: "", want: "required"},
		{name: "not json", args: "{", want: "valid JSON"},
		{name: "unknown field", args: `{"argv":["get"],"shell":"yes"}`, want: "schema"},
		{name: "empty argv item", args: `{"argv":["get",""]}`, want: "schema"},
		{name: "empty argv", args: `{"argv":[]}`, want: "schema"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := tool.Execute(context.Background(), json.RawMessage(test.args))
			if err == nil {
				t.Fatal("error = nil")
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %q, want containing %q", err, test.want)
			}
		})
	}
}

// 命令视图应对凭证类参数脱敏
func TestCommandViewRedact(t *testing.T) {
	got := commandView([]string{"--token=secret-value", "get", "pods", "--password", "p@ss"})
	if strings.Contains(got, "secret-value") || strings.Contains(got, "p@ss") {
		t.Fatalf("CommandView leaked secret: %q", got)
	}
	if !strings.Contains(got, "--token=***") || !strings.Contains(got, "--password ***") {
		t.Fatalf("CommandView = %q", got)
	}
}

// 工具应能注册进统一注册表并出现在规格列表中
func TestToolRegisterSpecs(t *testing.T) {
	tool := mustNewTool(t, Config{KubectlPath: writeFakeKubectl(t, "#!/bin/sh\nexit 0\n")})
	registry := tools.NewRegistry()
	if err := registry.Register(tool); err != nil {
		t.Fatalf("register: %v", err)
	}
	specs := registry.Specs()
	if len(specs) != 1 || specs[0].Name != toolName {
		t.Fatalf("specs = %#v", specs)
	}
	if len(specs[0].InputSchema) == 0 {
		t.Fatal("InputSchema empty")
	}
}

// 证明调用不经命令行外壳：参数中的元字符不会被解释
func TestToolNoShell(t *testing.T) {
	kubectl := writeFakeKubectl(t, `#!/bin/sh
printf '%s\n' "$@" > "$FAKE_KUBECTL_ARGV"
exit 0
`)
	tool := mustNewTool(t, Config{KubectlPath: kubectl})
	argvFile := filepath.Join(t.TempDir(), "argv.txt")
	t.Setenv("FAKE_KUBECTL_ARGV", argvFile)

	evil := "default; rm -rf /"
	_, err := tool.Execute(context.Background(), mustArgs(t, map[string]any{
		"argv": []string{"get", "pods", "-n", evil},
	}))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := readFile(t, argvFile)
	if !strings.Contains(got, evil) {
		t.Fatalf("argv not preserved literally: %q", got)
	}
	if strings.Count(got, "\n") != 4 {
		t.Fatalf("expected 4 argv lines, got %q", got)
	}
}

func mustNewTool(t *testing.T, cfg Config) *Tool {
	t.Helper()
	if cfg.DefaultTimeout == 0 {
		cfg.DefaultTimeout = 5 * time.Second
	}
	if cfg.MaxTimeout == 0 {
		cfg.MaxTimeout = 10 * time.Second
	}
	if cfg.MaxStdoutBytes == 0 {
		cfg.MaxStdoutBytes = 1 << 20
	}
	if cfg.MaxStderrBytes == 0 {
		cfg.MaxStderrBytes = 256 << 10
	}
	tool, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return tool
}

func mustArgs(t *testing.T, v map[string]any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	return raw
}

func decodeRaw(t *testing.T, raw json.RawMessage) resultRaw {
	t.Helper()
	var out resultRaw
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode raw: %v", err)
	}
	return out
}

func writeFakeKubectl(t *testing.T, script string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake kubectl shell scripts require unix")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	path := filepath.Join(t.TempDir(), "fake-kubectl")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake kubectl: %v", err)
	}
	return path
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// 文本表 Raw 可按 offset/limit 切片
func TestToolSliceTextTable(t *testing.T) {
	tool := mustNewTool(t, Config{})
	raw, _ := json.Marshal(resultRaw{
		Argv:     []string{"get", "pods"},
		ExitCode: 0,
		Stdout: "NAME   STATUS\n" +
			"p0     Running\n" +
			"p1     Error\n" +
			"p2     Pending\n",
	})
	view, err := tool.Slice(raw, tools.SliceQuery{Offset: 1, Limit: 1})
	if err != nil {
		t.Fatalf("slice: %v", err)
	}
	if view.Total != 3 || view.Offset != 1 || len(view.Rows) != 1 {
		t.Fatalf("view: %#v", view)
	}
	if view.Rows[0][0] != "p1" {
		t.Fatalf("row: %#v", view.Rows[0])
	}
}

// 非表格 stdout 走行级兜底：按物理行切片，空行保留，Columns 为空表示非表格
func TestToolSliceTextLines(t *testing.T) {
	tool := mustNewTool(t, Config{})
	raw, _ := json.Marshal(resultRaw{
		ExitCode: 0,
		Stdout:   "Name: demo-api\nNamespace: demo\n\nEvents:\n  Back-off\n",
	})
	view, err := tool.Slice(raw, tools.SliceQuery{Offset: 2, Limit: 2})
	if err != nil {
		t.Fatalf("slice: %v", err)
	}
	// 物理行共 5 行（含空行），窗口从第 2 行起
	if view.Total != 5 || view.Offset != 2 || len(view.Rows) != 2 {
		t.Fatalf("view: %#v", view)
	}
	if len(view.Columns) != 0 {
		t.Fatalf("want nil columns for non-table, got %#v", view.Columns)
	}
	if view.Rows[0][0] != "" || view.Rows[1][0] != "Events:" {
		t.Fatalf("rows: %#v", view.Rows)
	}
}

// 行级兜底的窗口饱和与越界：limit 钳到硬顶，offset 越界返回空页但 total 正确
func TestToolSliceTextLinesBounds(t *testing.T) {
	tool := mustNewTool(t, Config{})
	stdout := make([]string, 250)
	for i := range stdout {
		stdout[i] = fmt.Sprintf("line-%03d", i)
	}
	raw, _ := json.Marshal(resultRaw{ExitCode: 0, Stdout: strings.Join(stdout, "\n") + "\n"})

	// limit 超硬顶时钳到 200
	view, err := tool.Slice(raw, tools.SliceQuery{Offset: 0, Limit: 300})
	if err != nil {
		t.Fatalf("slice: %v", err)
	}
	if view.Limit != 200 || len(view.Rows) != 200 {
		t.Fatalf("clamped limit: limit=%d rows=%d", view.Limit, len(view.Rows))
	}

	// offset 越界返回空页，total 仍为全量行数
	view, err = tool.Slice(raw, tools.SliceQuery{Offset: 400, Limit: 10})
	if err != nil {
		t.Fatalf("slice: %v", err)
	}
	if view.Total != 250 || len(view.Rows) != 0 {
		t.Fatalf("out-of-range: total=%d rows=%d", view.Total, len(view.Rows))
	}
}

// 非零退出码不可切片
func TestToolSliceRejectsNonZeroExit(t *testing.T) {
	tool := mustNewTool(t, Config{})
	raw, _ := json.Marshal(resultRaw{ExitCode: 1, Stdout: "NAME  STATUS\na  b\n"})
	_, err := tool.Slice(raw, tools.SliceQuery{Offset: 0, Limit: 10})
	if err == nil {
		t.Fatal("expected exitCode error")
	}
}
