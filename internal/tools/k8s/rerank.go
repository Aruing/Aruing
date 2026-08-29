// C4 LLM 重排对照臂的重排器：用 llm 客户端构造 summary.RerankFunc
//
// 实验专用（默认不装配）：method=llm-rerank 时由装配层显式构造注入，
// 产品默认路径不感知模型（#19：投影纯机械；重排是对照臂的模型选行，选行后的
// 过滤/装入/标注仍在 summary 渲染层机械完成）
// 提示词从文件加载（#9，go:embed），不写死代码

package k8s

import (
	"context"
	_ "embed"
	"fmt"
	"strings"

	"github.com/Aruing/Aruing/internal/llm"
	"github.com/Aruing/Aruing/internal/tools/summary"
)

//go:embed prompts/rerank.md
var rerankPrompt string

// rerankOutput 模型输出的行号结构；行号 0 基，越界由渲染层机械过滤
type rerankOutput struct {
	Rows []int `json:"rows"`
}

// NewReranker 构造 C4 重排回调：输入全表与预算，让模型选出代表性行号
// 模型输出非法（无法解析 / 空选集）时返回错误——渲染层明确标注失败，不静默回退（#18）
// 无 ctx：llm 客户端自带整体超时；实验臂不污染渲染签名（见 summary.RerankFunc）
func NewReranker(client llm.Client) summary.RerankFunc {
	return func(columns []string, rows [][]string, budgetRunes int) ([]int, error) {
		var b strings.Builder
		fmt.Fprintf(&b, "预算：%d runes\n表共 %d 行。\n列: %s\n", budgetRunes, len(rows), strings.Join(columns, " "))
		for i, r := range rows {
			fmt.Fprintf(&b, "#%d  %s\n", i, strings.Join(r, "  "))
		}
		var out rerankOutput
		if err := client.GenerateJSON(context.Background(), llm.Request{System: rerankPrompt, User: b.String()}, &out); err != nil {
			return nil, fmt.Errorf("rerank llm: %w", err)
		}
		if len(out.Rows) == 0 {
			return nil, fmt.Errorf("rerank llm returned no rows")
		}
		return out.Rows, nil
	}
}
