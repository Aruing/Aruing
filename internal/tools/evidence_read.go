package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"aruing/internal/core"
	"aruing/internal/tools/summary"
)

const (
	evidenceReadToolName = "evidence.read"
	evidenceReadSource   = "evidence"
)

// 按 evidenceId 从轮内索引取 Raw，经源工具 Slicer 切出一页
// 只读导航；不复制 stdout 进模型上下文以外的路径；导航结果不回写索引
type EvidenceReadTool struct {
	index    *ObservationIndex
	registry *Registry
	schema   *jsonschema.Schema
	specJSON json.RawMessage
}

var evidenceReadInputSchema = json.RawMessage(`{
  "type": "object",
  "additionalProperties": false,
  "required": ["evidenceId", "offset", "limit"],
  "properties": {
    "evidenceId": {"type": "string", "minLength": 1},
    "offset": {"type": "integer", "minimum": 0},
    "limit": {"type": "integer", "minimum": 1, "maximum": 200}
  }
}`)

// 组装 evidence.read；index 与 registry 必填
func NewEvidenceReadTool(index *ObservationIndex, registry *Registry) (*EvidenceReadTool, error) {
	if index == nil {
		return nil, errors.New("evidence.read requires an observation index")
	}
	if registry == nil {
		return nil, errors.New("evidence.read requires a registry")
	}
	schema, err := compileEvidenceReadSchema(evidenceReadInputSchema)
	if err != nil {
		return nil, err
	}
	return &EvidenceReadTool{
		index:    index,
		registry: registry,
		schema:   schema,
		specJSON: append(json.RawMessage(nil), evidenceReadInputSchema...),
	}, nil
}

// 可发现规格：按 evidenceId 切片读已有观察
func (t *EvidenceReadTool) Spec() ToolSpec {
	return ToolSpec{
		Name: evidenceReadToolName,
		Description: "按 evidenceId 对已有工具观察做行级切片（offset/limit），不重新执行后端命令。" +
			"evidenceId 来自本轮 observations 中的 evidenceId 字段。" +
			"仅支持源工具实现了表格切片的观察（如 k8s 文本表 / JSON Table）；" +
			"不可切片时返回错误说明，可改用源工具重新查询（如 k8s 加 --field-selector / -o jsonpath）。",
		InputSchema: append(json.RawMessage(nil), t.specJSON...),
	}
}

// 从索引取 Raw → 源工具 Slicer → 渲染页摘要；导航结果不 Put 回索引
func (t *EvidenceReadTool) Execute(ctx context.Context, args json.RawMessage) (*core.Evidence, error) {
	if t == nil {
		return nil, errors.New("evidence.read tool is nil")
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}

	q, err := t.parseArgs(args)
	if err != nil {
		return nil, err
	}

	rec, ok := t.index.Get(q.EvidenceID)
	if !ok {
		return navErrorEvidence(q.EvidenceID, fmt.Sprintf(
			"evidenceId %q 不在本轮观察索引中（可能已结束或未产出 evidenceId）", q.EvidenceID)), nil
	}

	tool, getErr := t.registry.Get(rec.ToolName)
	if getErr != nil {
		return navErrorEvidence(q.EvidenceID, fmt.Sprintf(
			"源工具 %q 未注册，无法切片", rec.ToolName)), nil
	}
	slicer, ok := tool.(Slicer)
	if !ok {
		return navErrorEvidence(q.EvidenceID, fmt.Sprintf(
			"源工具 %q 不支持切片；请用该工具重新查询并收窄范围", rec.ToolName)), nil
	}

	view, sliceErr := slicer.Slice(rec.Raw, SliceQuery{Offset: q.Offset, Limit: q.Limit})
	if sliceErr != nil {
		return navErrorEvidence(q.EvidenceID, fmt.Sprintf(
			"无法切片（%s）；请用源工具重新查询（如加 --field-selector / -o jsonpath）", sliceErr.Error())), nil
	}

	label := fmt.Sprintf("evidence.read %s", q.EvidenceID)
	pageSummary := summary.Render(label, view.Columns, view.Rows, false)
	// 小表 Render 会全行写出；此处 rows 已是窗口。补充分页元信息
	meta := fmt.Sprintf("切片 · total=%d offset=%d limit=%d 本页=%d 行 · 源工具=%s",
		view.Total, view.Offset, view.Limit, len(view.Rows), rec.ToolName)
	fullSummary := meta + "\n" + pageSummary

	rawPayload, _ := json.Marshal(map[string]any{
		"evidenceId": q.EvidenceID,
		"toolName":   rec.ToolName,
		"total":      view.Total,
		"offset":     view.Offset,
		"limit":      view.Limit,
		"columns":    view.Columns,
		"rowCount":   len(view.Rows),
	})

	return &core.Evidence{
		Source:      evidenceReadSource,
		ToolName:    evidenceReadToolName,
		CommandView: fmt.Sprintf("evidence.read evidenceId=%s offset=%d limit=%d", q.EvidenceID, q.Offset, q.Limit),
		Summary:     strings.TrimRight(fullSummary, "\n"),
		Raw:         rawPayload,
	}, nil
}

type evidenceReadArgs struct {
	EvidenceID string `json:"evidenceId"`
	Offset     int    `json:"offset"`
	Limit      int    `json:"limit"`
}

func (t *EvidenceReadTool) parseArgs(args json.RawMessage) (evidenceReadArgs, error) {
	if len(bytes.TrimSpace(args)) == 0 {
		return evidenceReadArgs{}, errors.New("evidence.read arguments are required")
	}
	var probe any
	if err := json.Unmarshal(args, &probe); err != nil {
		return evidenceReadArgs{}, fmt.Errorf("evidence.read arguments are not valid JSON: %w", err)
	}
	if err := t.schema.Validate(probe); err != nil {
		return evidenceReadArgs{}, fmt.Errorf("evidence.read arguments: %w", err)
	}
	var q evidenceReadArgs
	if err := json.Unmarshal(args, &q); err != nil {
		return evidenceReadArgs{}, fmt.Errorf("evidence.read arguments: %w", err)
	}
	q.EvidenceID = strings.TrimSpace(q.EvidenceID)
	if q.EvidenceID == "" {
		return evidenceReadArgs{}, errors.New("evidenceId is required")
	}
	return q, nil
}

func navErrorEvidence(evidenceID, msg string) *core.Evidence {
	return &core.Evidence{
		Source:      evidenceReadSource,
		ToolName:    evidenceReadToolName,
		CommandView: "evidence.read " + evidenceID,
		Summary:     msg,
		Error:       msg,
		Raw:         json.RawMessage(`{}`),
	}
}

func compileEvidenceReadSchema(schema json.RawMessage) (*jsonschema.Schema, error) {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schema))
	if err != nil {
		return nil, fmt.Errorf("input schema: %w", err)
	}
	compiler := jsonschema.NewCompiler()
	const schemaURL = "evidence-read-input-schema.json"
	if err := compiler.AddResource(schemaURL, doc); err != nil {
		return nil, fmt.Errorf("input schema: %w", err)
	}
	compiled, err := compiler.Compile(schemaURL)
	if err != nil {
		return nil, fmt.Errorf("input schema: %w", err)
	}
	return compiled, nil
}
