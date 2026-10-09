package agent_test

// Tower 轮首回灌（0.1.4 persistence 步骤 3b）：账本证据带盘上留存引用的按账本
// 编号进本轮索引，重启态（新 Tower 实例 + 空索引）下旧超巨观察仍可 evidence.read
// 翻到内联截断点之后；回灌编号轮末随 Discard 清。

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Aruing/Aruing/internal/agent"
	"github.com/Aruing/Aruing/internal/core"
	"github.com/Aruing/Aruing/internal/session"
	"github.com/Aruing/Aruing/internal/store"
	"github.com/Aruing/Aruing/internal/tools"
)

// 盘读假 k8s：SliceSpool 从流切全量行（内联 Slice 不用于带引用观察）
type spoolLedgerK8sFake struct{}

func (spoolLedgerK8sFake) Spec() tools.ToolSpec {
	return tools.ToolSpec{Name: "k8s", Description: "fake k8s for spool rehydrate", InputSchema: json.RawMessage(`{"type":"object"}`)}
}

func (spoolLedgerK8sFake) Execute(_ context.Context, _ json.RawMessage) (*core.Evidence, error) {
	return &core.Evidence{ToolName: "k8s", Raw: json.RawMessage(`{}`)}, nil
}

func (spoolLedgerK8sFake) Slice([]byte, tools.SliceQuery) (tools.SliceView, error) {
	return tools.SliceView{}, fmt.Errorf("inline slice unused for spool-backed raw")
}

func (spoolLedgerK8sFake) SliceSpool(_ []byte, q tools.SliceQuery, spool io.Reader) (tools.SliceView, error) {
	content, err := io.ReadAll(spool)
	if err != nil {
		return tools.SliceView{}, err
	}
	lines := strings.Split(strings.TrimRight(string(content), "\n"), "\n")
	offset := q.Offset
	if offset < 0 {
		offset = 0
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 32
	}
	if offset > len(lines) {
		offset = len(lines)
	}
	end := offset + limit
	if end > len(lines) {
		end = len(lines)
	}
	rows := [][]string{}
	for _, ln := range lines[offset:end] {
		rows = append(rows, []string{ln})
	}
	return tools.SliceView{Total: len(lines), Offset: offset, Limit: limit, Rows: rows}, nil
}

// 内存假 spool 存储（只读路径）
type ledgerSpoolStore struct {
	files map[string]string
}

func (s *ledgerSpoolStore) Create(context.Context, string) (tools.SpoolFile, error) {
	return nil, fmt.Errorf("not used")
}

func (s *ledgerSpoolStore) Open(_ context.Context, sessionID, ref string) (io.ReadCloser, error) {
	content, ok := s.files[sessionID+"/"+ref]
	if !ok {
		return nil, fmt.Errorf("no spool %s/%s", sessionID, ref)
	}
	return io.NopCloser(strings.NewReader(content)), nil
}

// 重启态回灌：新 Tower + 空索引 + 账本带引用证据 → evidence.read 翻到截断点之后；
// 回灌编号轮末 Discard（跨轮不泄漏）
func TestTowerRehydratesSpooledLedgerEvidence(t *testing.T) {
	const (
		sessID   = "sess_rehydrate"
		evID     = "ev_ledger_1"
		spoolRef = "spool-ledger-1"
	)
	var full []string
	for i := 0; i < 100; i++ {
		full = append(full, fmt.Sprintf("line-%02d", i))
	}
	spool := &ledgerSpoolStore{files: map[string]string{
		sessID + "/" + spoolRef: strings.Join(full, "\n") + "\n",
	}}
	// 账本证据：内联 stdout 只有前两行，引用指向盘上全量 100 行
	raw := json.RawMessage(fmt.Sprintf(
		`{"argv":["logs","p"],"exitCode":0,"stdout":%q,"stdoutTruncated":true,"stdoutSpool":{"sessionId":%q,"file":%q,"totalBytes":4096,"totalLines":100}}`,
		"line-00\nline-01\n", sessID, spoolRef,
	))
	ledger := store.NewMemoryRunLedger()
	if err := ledger.Put(context.Background(), session.DiagnosticRecord{
		RunID:     "run_ledger_1",
		SessionID: sessID,
		Question:  "why down",
		Evidence: []core.Evidence{
			{ID: evID, RunID: "run_ledger_1", ToolName: "k8s", Raw: raw},
			{ID: "ev_plain", RunID: "run_ledger_1", ToolName: "k8s", Raw: json.RawMessage(`{"stdout":"ok\n"}`)},
		},
	}); err != nil {
		t.Fatalf("ledger put: %v", err)
	}

	var secondUser string
	var calls atomic.Int32
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n == 1 {
			// 不查新工具，直接对账本证据编号翻页（越过内联截断点）
			writeChatCompletion(w, fmt.Sprintf(
				`{"action":"call_tool","tool_call":{"tool_name":"evidence.read","arguments":{"evidenceId":%q,"offset":50,"limit":2},"purpose":"翻旧超巨证据"}}`,
				evID,
			))
			return
		}
		var reqBody struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&reqBody)
		for _, m := range reqBody.Messages {
			if m.Role == "user" {
				secondUser = m.Content
			}
		}
		writeChatCompletion(w, `{"action":"reply","content":"done","question":""}`)
	})

	idx := tools.NewObservationIndex()
	registry := tools.NewRegistry()
	if err := registry.Register(spoolLedgerK8sFake{}); err != nil {
		t.Fatalf("register k8s fake: %v", err)
	}
	evRead, err := tools.NewEvidenceReadTool(idx, registry, spool)
	if err != nil {
		t.Fatalf("evidence.read: %v", err)
	}
	if regErr := registry.Register(evRead); regErr != nil {
		t.Fatalf("register evidence.read: %v", regErr)
	}

	// 重启态：新 Tower 实例，索引为空，全部依赖同账本/同 spool 内容
	tower, err := agent.NewTowerResponder(client, newTestFactory(t), &fakeRunExecutor{}, ledger,
		tools.NewDispatcher(registry, tools.NewReadonlyPolicy()), registry.Specs(), idx)
	if err != nil {
		t.Fatalf("new tower: %v", err)
	}

	if _, err := tower.Respond(context.Background(), session.RespondInput{
		SessionID: sessID,
		UserText:  "上次那个超巨日志第 50 行是什么",
	}); err != nil {
		t.Fatalf("respond: %v", err)
	}

	// 翻页结果回喂进第二轮载荷：截断点之后的行可见（回灌 + 盘读全链成立）
	for _, want := range []string{"line-50", "line-51", "total=100"} {
		if !strings.Contains(secondUser, want) {
			t.Fatalf("second payload missing %q (rehydrate chain broken):\n%s", want, secondUser)
		}
	}
	// 回灌编号轮末 Discard：跨轮不泄漏
	if _, ok := idx.Get(evID); ok {
		t.Fatalf("rehydrated id %q must be discarded after Respond", evID)
	}
}
