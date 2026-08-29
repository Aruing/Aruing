package k8s

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Aruing/Aruing/internal/llm"
)

// 假 llm 客户端：记录请求并回放固定正文（或错误）
type fakeRerankLLM struct {
	content string
	err     error
	seen    llm.Request
}

func (f *fakeRerankLLM) Generate(_ context.Context, req llm.Request) (llm.Response, error) {
	return llm.Response{Content: f.content}, f.err
}

func (f *fakeRerankLLM) GenerateJSON(_ context.Context, req llm.Request, out any) error {
	f.seen = req
	if f.err != nil {
		return f.err
	}
	return json.Unmarshal([]byte(f.content), out)
}

// 重排器：假 client 回行号 JSON → 选行正确；输入按「预算 + 带行号表」约定组装
func TestNewReranker(t *testing.T) {
	cols := []string{"NAME", "STATUS"}
	rows := make([][]string, 10)
	for i := range rows {
		rows[i] = []string{strings.Repeat("p", 1) + string(rune('0'+i)), "Running"}
	}
	f := &fakeRerankLLM{content: `{"rows":[3,7]}`}
	idxs, err := NewReranker(f)(cols, rows, 1024)
	if err != nil {
		t.Fatalf("rerank: %v", err)
	}
	if len(idxs) != 2 || idxs[0] != 3 || idxs[1] != 7 {
		t.Fatalf("选行不符：%v", idxs)
	}
	if !strings.Contains(f.seen.User, "预算：1024") || !strings.Contains(f.seen.User, "#3") {
		t.Fatalf("输入应含预算与带行号的表：\n%s", f.seen.User)
	}
	if !strings.Contains(f.seen.System, "rows") {
		t.Fatalf("系统提示应来自 rerank.md（go:embed）")
	}
}

// 重排器错误路径：非法输出与空选集都明确报错（渲染层标注失败，不静默回退）
func TestNewRerankerErrors(t *testing.T) {
	cols := []string{"NAME"}
	rows := [][]string{{"a"}, {"b"}}
	bad := &fakeRerankLLM{content: "not json"}
	if _, err := NewReranker(bad)(cols, rows, 512); err == nil {
		t.Fatalf("非法输出应报错")
	}
	empty := &fakeRerankLLM{content: `{"rows":[]}`}
	if _, err := NewReranker(empty)(cols, rows, 512); err == nil {
		t.Fatalf("空选集应报错")
	}
	failing := &fakeRerankLLM{err: context.DeadlineExceeded}
	if _, err := NewReranker(failing)(cols, rows, 512); err == nil {
		t.Fatalf("调用失败应报错")
	}
}
