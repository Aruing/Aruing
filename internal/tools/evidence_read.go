package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/Aruing/Aruing/internal/core"
	"github.com/Aruing/Aruing/internal/tools/summary"
)

const (
	evidenceReadToolName = "evidence.read"
	evidenceReadSource   = "evidence"
	// 行模式单行渲染截断上限（runes）：logs 单行可极长，截断加省略号，全文仍在源证据 Raw（#18 不静默丢）
	lineRenderTruncateRunes = 240
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
    "limit": {"type": "integer", "minimum": 1, "maximum": 200},
    "since": {"type": "string", "minLength": 1},
    "until": {"type": "string", "minLength": 1}
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
			"表格（k8s 文本表 / JSON Table）与 describe/logs/events 等非表格输出均可切片；" +
			"非表格切片逐行返回并带行号，单行超长会截断标注。" +
			"带时间戳的 logs 观察可用可选 since/until（RFC3339 闭区间）先按时间窗过滤再翻页；" +
			"取 logs 时建议源工具加 --timestamps，否则不可时间切（报错时会提示）。" +
			"logs 大输出建议先用源工具加 --since-time / --tail 收窄。" +
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

	view, sliceErr := slicer.Slice(rec.Raw, SliceQuery{
		Offset: q.Offset,
		Limit:  q.Limit,
		Since:  q.Since,
		Until:  q.Until,
	})
	if sliceErr != nil {
		return navErrorEvidence(q.EvidenceID, fmt.Sprintf(
			"无法切片（%s）；请用源工具重新查询（如加 --field-selector / -o jsonpath）", sliceErr.Error())), nil
	}

	label := fmt.Sprintf("evidence.read %s", q.EvidenceID)
	// 非表格切片（Columns 为空）走行渲染：避免大表抽样逻辑误伤行模式，也带行号便于继续翻页
	var pageSummary string
	var truncatedLines int
	if len(view.Columns) == 0 {
		pageSummary, truncatedLines = renderLines(view)
	} else {
		// 小表 Render 会全行写出；此处 rows 已是窗口
		pageSummary = summary.Render(label, view.Columns, view.Rows, false)
	}
	meta := fmt.Sprintf("切片 · total=%d offset=%d limit=%d 本页=%d 行 · 源工具=%s",
		view.Total, view.Offset, view.Limit, len(view.Rows), rec.ToolName)
	if q.Since != "" || q.Until != "" {
		meta += fmt.Sprintf(" · 时间窗 since=%s until=%s", orDash(q.Since), orDash(q.Until))
		// 后端回填窗内首末时间戳，供模型判断窗口是否切中、要不要再推进
		if view.WindowFirst != "" || view.WindowLast != "" {
			meta += fmt.Sprintf(" · 窗内首行=%s 末行=%s", orDash(view.WindowFirst), orDash(view.WindowLast))
		} else {
			meta += " · 窗内无匹配行"
		}
	}
	if truncatedLines > 0 {
		meta += fmt.Sprintf(" · %d 行超长截断（全文在源证据 raw）", truncatedLines)
	}
	fullSummary := meta + "\n" + pageSummary

	rawPayload, _ := json.Marshal(map[string]any{
		"evidenceId": q.EvidenceID,
		"toolName":   rec.ToolName,
		"total":      view.Total,
		"offset":     view.Offset,
		"limit":      view.Limit,
		"since":      q.Since,
		"until":      q.Until,
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
	// 可选时间窗（RFC3339 闭区间），透传给源工具 Slicer 先过滤再开窗
	Since string `json:"since"`
	Until string `json:"until"`
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
	// RFC3339 形状校验（schema 只拦非空字符串）；非法格式尽早失败，不留给后端猜
	for name, v := range map[string]string{"since": q.Since, "until": q.Until} {
		if v == "" {
			continue
		}
		if _, err := time.Parse(time.RFC3339, v); err != nil {
			return evidenceReadArgs{}, fmt.Errorf("%s must be RFC3339 (got %q): %w", name, v, err)
		}
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

// 空值渲染为占位横杠，时间窗元信息用
func orDash(v string) string {
	if v == "" {
		return "-"
	}
	return v
}

// 行模式渲染：非表格切片逐行输出，每行前缀绝对行号，模型可据此定位下一窗
// 单行超 lineRenderTruncateRunes 截断加省略号；返回被截断的行数供元信息标注，全文仍在源证据 Raw
func renderLines(view SliceView) (string, int) {
	var b strings.Builder
	truncated := 0
	for i, row := range view.Rows {
		line := ""
		if len(row) > 0 {
			line = row[0]
		}
		r := []rune(line)
		if len(r) > lineRenderTruncateRunes {
			r = r[:lineRenderTruncateRunes]
			truncated++
			line = string(r) + "…"
		}
		fmt.Fprintf(&b, "%d: %s\n", view.Offset+i, line)
	}
	return strings.TrimRight(b.String(), "\n"), truncated
}

func compileEvidenceReadSchema(schema json.RawMessage) (*jsonschema.Schema, error) {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schema))
	if err != nil {
		return nil, fmt.Errorf("input schema: %w", err)
	}
	compiler := jsonschema.NewCompiler()
	const schemaURL = "evidence-read-input-schema.json"
	if err = compiler.AddResource(schemaURL, doc); err != nil {
		return nil, fmt.Errorf("input schema: %w", err)
	}
	compiled, err := compiler.Compile(schemaURL)
	if err != nil {
		return nil, fmt.Errorf("input schema: %w", err)
	}
	return compiled, nil
}
