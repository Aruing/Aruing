// 机械结果归类：观测文本对动作结果类别的包含匹配（裁决 4：机械解析优先）
//
// 只做大小写归一的包含匹配，不做打分排序（启发式选优 = 业务判断越界，#19 同构）；
// 唯一命中才可用——零命中或多命中返回 false，由调用方走富文本强度路径兜底
// （问用户答复无富文本路径，零/多命中不更新，见 acquireLoop 注释）
package agent

import "strings"

// 文本对结果类别的唯一命中归类
//
// outcomes 为动作的判别矩阵列名（规划器产出的短标签）；文本为证据可观察内容
// 或用户答复原文。短标签（如单字符）会天然多命中落到不可归类，由唯一性约束自限
func classifyOutcome(text string, outcomes []string) (string, bool) {
	lowered := strings.ToLower(text)
	var hit string
	for _, o := range outcomes {
		if o == "" {
			continue
		}
		if strings.Contains(lowered, strings.ToLower(o)) {
			if hit != "" {
				return "", false // 多命中：不可唯一归类
			}
			hit = o
		}
	}
	if hit == "" {
		return "", false // 零命中
	}
	return hit, true
}
