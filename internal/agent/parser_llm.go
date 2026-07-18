// 真实解析器把运行中的原始问题交给大模型，再回填系统编号和时间
//
// 与 FakeParser 共享同一调用边界，Orchestrator 可按依赖配置透明替换
// 调用方必须传入有效的运行数据和可取消上下文，结果只包含未验证线索
package agent

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"strings"

	"aruing/internal/core"
	"aruing/internal/llm"
)

// 编译时嵌入 parser 角色的提示词，保持 prompt 与代码分离，又避免运行时读文件
// 修改 prompts/parser.md 后重新 build 即可生效，无需改本文件
//
//go:embed prompts/parser.md
var parserPrompt string

// 用大模型把用户问题解析为结构化线索的解析器
//
// 持有不可变依赖（模型客户端、元数据工厂、prompt 文本），可被多次运行复用
// 不持有跨运行的共享可变状态，重试与并发调用安全
type LLMParser struct {
	client  llm.Client
	factory *core.Factory
	prompt  string
}

// 创建基于大模型的解析器，prompt 从包内嵌入文件加载，不暴露给调用方
// 任一依赖缺失直接返回错误，避免运行期才暴露初始化问题
func NewLLMParser(client llm.Client, factory *core.Factory) (*LLMParser, error) {
	if client == nil {
		return nil, errors.New("LLM parser requires an llm client")
	}
	if factory == nil {
		return nil, errors.New("LLM parser requires a factory")
	}
	return &LLMParser{client: client, factory: factory, prompt: parserPrompt}, nil
}

// 把原始问题交给大模型，返回绑定当前运行编号的问题结构
// 系统编号（query/node/edge）由工厂统一生成，模型只负责产出线索内容
func (p *LLMParser) Parse(ctx context.Context, run core.Run) (core.Query, error) {
	if ctx == nil {
		return core.Query{}, errors.New("parser requires a context")
	}
	if err := ctx.Err(); err != nil {
		return core.Query{}, fmt.Errorf("parse query: %w", err)
	}
	if p == nil {
		return core.Query{}, errors.New("parser is required")
	}
	if strings.TrimSpace(run.ID) == "" {
		return core.Query{}, errors.New("parser requires a run ID")
	}
	if strings.TrimSpace(run.Question) == "" {
		return core.Query{}, errors.New("parser requires a question")
	}

	queryID, err := p.factory.NewID("query")
	if err != nil {
		return core.Query{}, fmt.Errorf("create query ID: %w", err)
	}

	var output parserOutput
	req := llm.Request{
		System: p.prompt,
		User:   run.Question,
	}
	if err := p.client.GenerateJSON(ctx, req, &output); err != nil {
		return core.Query{}, fmt.Errorf("parse question with LLM: %w", err)
	}

	if err := validateParserOutput(output); err != nil {
		return core.Query{}, fmt.Errorf("parse output: %w", err)
	}

	query := core.Query{
		ID:        queryID,
		RunID:     run.ID,
		Goal:      output.Goal,
		Nodes:     make([]core.Node, 0, len(output.Nodes)),
		CreatedAt: p.factory.Now(),
	}

	// 模型用 ref 引用节点，回填系统编号时建立 ref->id 映射，供后续 edge 解析使用
	refToID := make(map[string]string, len(output.Nodes))
	for _, node := range output.Nodes {
		id, err := p.factory.NewID("node")
		if err != nil {
			return core.Query{}, fmt.Errorf("create node ID: %w", err)
		}
		refToID[node.Ref] = id
		query.Nodes = append(query.Nodes, core.Node{
			ID:    id,
			Type:  node.Type,
			Text:  node.Text,
			Attrs: node.Attrs,
		})
	}

	if len(output.Edges) > 0 {
		query.Edges = make([]core.Edge, 0, len(output.Edges))
		for _, edge := range output.Edges {
			from, ok := refToID[edge.From]
			if !ok {
				return core.Query{}, fmt.Errorf("edge references unknown node ref %q", edge.From)
			}
			to, ok := refToID[edge.To]
			if !ok {
				return core.Query{}, fmt.Errorf("edge references unknown node ref %q", edge.To)
			}
			id, err := p.factory.NewID("edge")
			if err != nil {
				return core.Query{}, fmt.Errorf("create edge ID: %w", err)
			}
			query.Edges = append(query.Edges, core.Edge{
				ID:    id,
				From:  from,
				To:    to,
				Type:  edge.Type,
				Attrs: edge.Attrs,
			})
		}
	}

	if strings.TrimSpace(output.Since) != "" {
		query.TimeRange = &core.TimeRange{Since: output.Since}
	}

	return query, nil
}

// 校验模型输出满足后续模块的最小契约，避免半成品进入诊断流
func validateParserOutput(out parserOutput) error {
	if strings.TrimSpace(out.Goal) == "" {
		return errors.New("goal is required")
	}
	if len(out.Nodes) == 0 {
		return errors.New("at least one node is required")
	}
	for i, node := range out.Nodes {
		if strings.TrimSpace(node.Ref) == "" {
			return fmt.Errorf("node[%d] ref is required", i)
		}
	}
	return nil
}

// 模型返回的中间结构，只包含线索内容，不含任何系统编号或时间字段
// node.Ref/edge.From/edge.To 是局部引用，由 Parse 统一替换为系统编号
type parserOutput struct {
	Goal  string       `json:"goal"`
	Nodes []parserNode `json:"nodes"`
	Edges []parserEdge `json:"edges"`
	Since string       `json:"since"`
}

type parserNode struct {
	Ref   string            `json:"ref"`
	Type  string            `json:"type"`
	Text  string            `json:"text"`
	Attrs map[string]string `json:"attrs,omitempty"`
}

type parserEdge struct {
	From  string            `json:"from"`
	To    string            `json:"to"`
	Type  string            `json:"type"`
	Attrs map[string]string `json:"attrs,omitempty"`
}
