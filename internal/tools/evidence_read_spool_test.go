package tools_test

// evidence.read 盘读翻页（0.1.4 persistence 步骤 3b）：Raw 带 stdoutSpool 引用时
// 优先走盘上全量流切页，可翻到内联截断点之后；失败路径统一 navError 引导重查。

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/Aruing/Aruing/internal/core"
	"github.com/Aruing/Aruing/internal/tools"
)

// 内存假 spool 存储：按 sessionID/file 存内容，可注入 Open 故障
type memSpoolStore struct {
	files   map[string]string
	openErr error
}

func (s *memSpoolStore) Create(context.Context, string) (tools.SpoolFile, error) {
	return nil, errors.New("not used in read-path tests")
}

func (s *memSpoolStore) Open(_ context.Context, sessionID, ref string) (io.ReadCloser, error) {
	if s.openErr != nil {
		return nil, s.openErr
	}
	content, ok := s.files[sessionID+"/"+ref]
	if !ok {
		return nil, fmt.Errorf("no spool %s/%s", sessionID, ref)
	}
	return io.NopCloser(bytes.NewReader([]byte(content))), nil
}

// 假源工具：内联 Slice 报错（证明带引用观察不再依赖内联实现），
// SliceSpool 从盘上流切全量行
type spoolBackedFake struct {
	name string
}

func (f *spoolBackedFake) Spec() tools.ToolSpec {
	return tools.ToolSpec{Name: f.name, Description: "fake spool slicer", InputSchema: []byte(`{"type":"object"}`)}
}

func (f *spoolBackedFake) Execute(context.Context, json.RawMessage) (*core.Evidence, error) {
	return &core.Evidence{ToolName: f.name, Raw: []byte(`{}`)}, nil
}

func (f *spoolBackedFake) Slice([]byte, tools.SliceQuery) (tools.SliceView, error) {
	return tools.SliceView{}, errors.New("inline slice must not be used for spool-backed raw")
}

func (f *spoolBackedFake) SliceSpool(raw []byte, q tools.SliceQuery, spool io.Reader) (tools.SliceView, error) {
	content, err := io.ReadAll(spool)
	if err != nil {
		return tools.SliceView{}, err
	}
	lines := strings.Split(strings.TrimRight(string(content), "\n"), "\n")
	offset, limit := q.Offset, q.Limit
	if offset < 0 {
		offset = 0
	}
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

// 组装带 spool 引用的 k8s 形态 Raw（内联 stdout 截断版）
func spoolBackedRaw(t *testing.T, sessionID, file, inline string, totalLines int) json.RawMessage {
	t.Helper()
	return json.RawMessage(fmt.Sprintf(
		`{"argv":["logs","p"],"exitCode":0,"stdout":%q,"stdoutTruncated":true,"stdoutSpool":{"sessionId":%q,"file":%q,"totalBytes":4096,"totalLines":%d}}`,
		inline, sessionID, file, totalLines,
	))
}

// 带引用观察 → 盘读翻页：越过内联截断点的行可读，元信息标注盘上翻页
func TestEvidenceReadSpoolPathPages(t *testing.T) {
	var full []string
	for i := 0; i < 100; i++ {
		full = append(full, fmt.Sprintf("line-%02d", i))
	}
	spool := &memSpoolStore{files: map[string]string{
		"sess_sp/spool-1": strings.Join(full, "\n") + "\n",
	}}
	fake := &spoolBackedFake{name: "k8s"}

	idx := tools.NewObservationIndex()
	reg := tools.NewRegistry()
	if err := reg.Register(fake); err != nil {
		t.Fatalf("register: %v", err)
	}
	idx.Put("e_big", tools.ObsRecord{
		Raw:      spoolBackedRaw(t, "sess_sp", "spool-1", "line-00\nline-01\n", 100),
		ToolName: "k8s",
	})

	tool, err := tools.NewEvidenceReadTool(idx, reg, spool)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ev, err := tool.Execute(context.Background(), mustJSON(t, map[string]any{
		"evidenceId": "e_big", "offset": 50, "limit": 2,
	}))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if ev.Error != "" {
		t.Fatalf("unexpected error: %s", ev.Error)
	}
	for _, want := range []string{"total=100", "offset=50", "盘上留存翻页", "line-50", "line-51"} {
		if !strings.Contains(ev.Summary, want) {
			t.Fatalf("summary missing %q:\n%s", want, ev.Summary)
		}
	}
}

// 内存形态（spools=nil）遇引用观察：明确 navError 引导重查，不冒充可读
func TestEvidenceReadSpoolPathRequiresStore(t *testing.T) {
	idx := tools.NewObservationIndex()
	reg := tools.NewRegistry()
	fake := &spoolBackedFake{name: "k8s"}
	if err := reg.Register(fake); err != nil {
		t.Fatalf("register: %v", err)
	}
	idx.Put("e_big", tools.ObsRecord{Raw: spoolBackedRaw(t, "s", "f", "x\n", 1), ToolName: "k8s"})

	tool, err := tools.NewEvidenceReadTool(idx, reg, nil)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ev, err := tool.Execute(context.Background(), mustJSON(t, map[string]any{
		"evidenceId": "e_big", "offset": 0, "limit": 1,
	}))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if ev.Error == "" || !strings.Contains(ev.Error, "内存形态") || !strings.Contains(ev.Error, "重新查询") {
		t.Fatalf("want memory-mode guidance, got: %s", ev.Error)
	}
}

// 引用文件缺失：navError 引导重查
func TestEvidenceReadSpoolOpenFailure(t *testing.T) {
	spool := &memSpoolStore{files: map[string]string{}, openErr: errors.New("disk gone")}
	idx := tools.NewObservationIndex()
	reg := tools.NewRegistry()
	if err := reg.Register(&spoolBackedFake{name: "k8s"}); err != nil {
		t.Fatalf("register: %v", err)
	}
	idx.Put("e_big", tools.ObsRecord{Raw: spoolBackedRaw(t, "s", "f", "x\n", 1), ToolName: "k8s"})

	tool, err := tools.NewEvidenceReadTool(idx, reg, spool)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ev, err := tool.Execute(context.Background(), mustJSON(t, map[string]any{
		"evidenceId": "e_big", "offset": 0, "limit": 1,
	}))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if ev.Error == "" || !strings.Contains(ev.Error, "盘上留存读取失败") {
		t.Fatalf("want open-failure guidance, got: %s", ev.Error)
	}
}

// 源工具不实现 SpoolSlicer：navError 引导（SliceSpool 路径的能力检查）
func TestEvidenceReadSpoolSourceNotSpoolSlicer(t *testing.T) {
	idx := tools.NewObservationIndex()
	reg := tools.NewRegistry()
	if err := reg.Register(&sliceableFake{name: "fake.table"}); err != nil {
		t.Fatalf("register: %v", err)
	}
	idx.Put("e_big", tools.ObsRecord{Raw: spoolBackedRaw(t, "s", "f", "x\n", 1), ToolName: "fake.table"})

	tool, err := tools.NewEvidenceReadTool(idx, reg, &memSpoolStore{files: map[string]string{"s/f": "x\n"}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ev, err := tool.Execute(context.Background(), mustJSON(t, map[string]any{
		"evidenceId": "e_big", "offset": 0, "limit": 1,
	}))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if ev.Error == "" || !strings.Contains(ev.Error, "不支持盘上切片") {
		t.Fatalf("want not-spool-slicer guidance, got: %s", ev.Error)
	}
}

// 旧格式 Raw（无引用字段）：走内联 Slice 老路径，零回归
func TestEvidenceReadOldRawSkipsSpoolPath(t *testing.T) {
	idx := tools.NewObservationIndex()
	reg := tools.NewRegistry()
	fake := &spoolBackedFake{name: "fake.table"}
	if err := reg.Register(fake); err != nil {
		t.Fatalf("register: %v", err)
	}
	// 无 stdoutSpool 字段：spoolBackedFake.Slice 会报错，若误入盘读/内联错路径即失败
	idx.Put("e_old", tools.ObsRecord{Raw: json.RawMessage(`{"stdout":"NAME\np0\n"}`), ToolName: "fake.table"})

	tool, err := tools.NewEvidenceReadTool(idx, reg, &memSpoolStore{files: map[string]string{}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	// 无 stdoutSpool 字段：走内联老路径。spoolBackedFake 的内联 Slice 报错，
	// 若路径选择正确会得到「无法切片」navError（而非盘读路径的引用类文案）
	ev, err := tool.Execute(context.Background(), mustJSON(t, map[string]any{
		"evidenceId": "e_old", "offset": 0, "limit": 1,
	}))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if ev.Error == "" || !strings.Contains(ev.Error, "无法切片") {
		t.Fatalf("want inline-path slice guidance, got: %s", ev.Error)
	}
	if strings.Contains(ev.Error, "盘上") {
		t.Fatalf("old raw must not enter spool path: %s", ev.Error)
	}
}
