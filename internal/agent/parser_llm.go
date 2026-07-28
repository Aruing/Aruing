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

// 业务级重试次数上限：模型可能偶发产出结构合法但语义违规的输出（如 ref 重复）
// 每次 attempt 内部 LLM 客户端还会按其配置做网络层重试，所以这里只覆盖"持续不合规"
const maxParseAttempts = 3

// 业务级重试耗尽时返回
//
// 调用方可以据此区分"网络抖动"和"模型持续产出不合规输出"：
//   - 前者由 internal/llm.Client 自己重试后返回 transport 类 error
//   - 后者由本包用 ErrLLMOutputInconsistent 包裹，提示 prompt 或模型能力问题
//
// errors.Is(err, ErrLLMOutputInconsistent) 为 true 时，建议检查 prompt 是否被改坏或更换模型
var ErrLLMOutputInconsistent = errors.New("llm output inconsistent after retries")

// 用大模型把用户问题解析为结构化线索的解析器
//
// 持有不可变依赖（模型客户端、元数据工厂、prompt 文本），可被多次运行复用
// 不持有跨运行的共享可变状态，重试与并发调用安全
type LLMParser struct {
	// 大模型客户端，负责发 prompt 收 JSON
	client llm.Client
	// 领域编号与创建时间工厂，回填 Query/Node/Edge 时使用
	factory *core.Factory
	// 嵌入的系统提示词全文，构造后只读
	prompt string
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
//
// 系统编号（query/node/edge）由工厂统一生成，模型只负责产出线索内容
// 模型若返回 ref 重复、缺 goal 等语义违规输出，会在业务级重试内重新请求；
// 重试 maxParseAttempts 次仍不合规则返回 ErrLLMOutputInconsistent
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

	req := llm.Request{
		System: p.prompt,
		User:   run.Question,
	}

	// 业务级重试：模型偶尔会忽略硬约束（如重复 ref），重新请求通常能恢复
	// 网络层错误由 llm.Client 自己重试，本循环只覆盖"模型合规性"问题
	var lastOutput parserOutput
	var lastValidateErr error
	for attempt := 0; attempt < maxParseAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return core.Query{}, fmt.Errorf("parse query: %w", err)
		}

		// 每轮用零值 output 接收，避免上一轮残留字段污染下一轮校验
		var output parserOutput
		if gErr := p.client.GenerateJSON(ctx, req, &output); gErr != nil {
			// 运输层错误（含 LLM 客户端自身的重试耗尽）直接上抛，不在业务层重试
			return core.Query{}, fmt.Errorf("parse question with LLM: %w", gErr)
		}

		if vErr := validateParserOutput(output); vErr != nil {
			lastOutput = output
			lastValidateErr = vErr
			continue
		}

		return p.fillQuery(run, queryID, output)
	}

	// 重试耗尽：把最后一次产出和原因带出去，方便定位是 prompt 还是模型能力问题
	return core.Query{}, fmt.Errorf("%w: last error: %v, last output: %+v",
		ErrLLMOutputInconsistent, lastValidateErr, lastOutput)
}

// 把校验通过的模型输出回填为带系统编号的 Query
//
// 模型用 ref 引用节点，回填时建立 ref→id 映射供 edge 解析使用
// 校验已保证 ref 唯一，此处不再重复查重；edge.From/To 仍需查表以防模型越界引用
func (p *LLMParser) fillQuery(run core.Run, queryID string, output parserOutput) (core.Query, error) {
	query := core.Query{
		ID:        queryID,
		RunID:     run.ID,
		Goal:      output.Goal,
		Nodes:     make([]core.Node, 0, len(output.Nodes)),
		CreatedAt: p.factory.Now(),
	}

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
//
// 校验项：
//   - goal 非空
//   - nodes 至少 1 个
//   - 每个 node.ref 非空且唯一（重复 ref 会让回填静默覆盖，造成上下文关系混乱）
//
// 缺失或重复都返回错误，调用方（Parse）会触发业务级重试
func validateParserOutput(out parserOutput) error {
	if strings.TrimSpace(out.Goal) == "" {
		return errors.New("goal is required")
	}
	if len(out.Nodes) == 0 {
		return errors.New("at least one node is required")
	}
	seen := make(map[string]struct{}, len(out.Nodes))
	for i, node := range out.Nodes {
		if strings.TrimSpace(node.Ref) == "" {
			return fmt.Errorf("node[%d] ref is required", i)
		}
		if _, dup := seen[node.Ref]; dup {
			return fmt.Errorf("node[%d] ref %q is duplicated", i, node.Ref)
		}
		seen[node.Ref] = struct{}{}
	}
	return nil
}

// 模型返回的中间结构，只包含线索内容，不含任何系统编号或时间字段
// node.Ref/edge.From/edge.To 是局部引用，由 Parse 统一替换为系统编号
type parserOutput struct {
	// 诊断目标的一句话概括，必填
	Goal string `json:"goal"`
	// 从问题中抽出的线索节点列表，至少一项
	Nodes []parserNode `json:"nodes"`
	// 节点之间的有向关系，可空
	Edges []parserEdge `json:"edges"`
	// 可选时间下界原文（如 "1h"），非空时写入 Query.TimeRange.Since
	Since string `json:"since"`
}

// 模型侧节点线索，Ref 仅在本输出内唯一，回填时换成系统 Node.ID
type parserNode struct {
	// 局部引用名，供本输出内边 From/To 引用，必须非空且唯一
	Ref string `json:"ref"`
	// 节点类型开放字符串，如 resource、symptom
	Type string `json:"type"`
	// 节点展示文本，通常是用户提到的资源名或症状
	Text string `json:"text"`
	// 附加属性，键可用 k8s.* / hint.* 等前缀，可空
	Attrs map[string]string `json:"attrs,omitempty"`
}

// 模型侧有向边，From/To 指向本输出内的 node.ref
type parserEdge struct {
	// 起点节点的局部 ref
	From string `json:"from"`
	// 终点节点的局部 ref
	To string `json:"to"`
	// 关系类型开放字符串，如 calls、depends_on
	Type string `json:"type"`
	// 关系附加属性，可空
	Attrs map[string]string `json:"attrs,omitempty"`
}
