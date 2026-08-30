// C1 地址无损兜底：有损变换出口的机械地址校验与补附
//
// 任何压缩产物（截断预览 / 折叠骨架 / LLM 交接摘要）都必须保留原段的全部地址：
// 机械抽取（ID 族正则 + 可选词典实体）比对，缺则以 [addr_refs] 行补附——
// 不依赖模型自觉（#19 纯机械）；地址宁多勿丢，footer 不做裁剪（#18）
package agent

import (
	"regexp"
	"strings"
)

// 地址 ID 族正则：core.Factory 生产代码全部前缀（run/e/h/t/v/rep/node/edge/target/query/sess/msg）
// 新增 Factory 前缀时必须同步本表；误报（正文碰巧形如 t_xxx）只让 footer 多一条，不丢信息
var addrIDRe = regexp.MustCompile(`\b(?:run|e|h|t|v|rep|node|edge|target|query|sess|msg)_[0-9a-z]+`)

// 补附行前缀；行内地址会被再次抽取，天然保证幂等（重复校验不重复追加）
const addrRefsPrefix = "[addr_refs] "

// 按出现序去重抽取文本中的 ID 族地址
// 命中尾部还接词字符（字母/数字/下划线）时说明是更长标识符的前缀，跳过
// （RE2 无环视，判尾手工补；ID 字符集为小写，后接大写同理不算命中）
func extractIDAddrs(text string) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0)
	for _, loc := range addrIDRe.FindAllStringIndex(text, -1) {
		if end := loc[1]; end < len(text) && isWordByte(text[end]) {
			continue
		}
		m := text[loc[0]:loc[1]]
		if _, ok := seen[m]; ok {
			continue
		}
		seen[m] = struct{}{}
		out = append(out, m)
	}
	return out
}

// ASCII 词字节：字母、数字或下划线
func isWordByte(c byte) bool {
	return c == '_' || (c >= '0' && c <= '9') ||
		(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// 抽取文本中的全部地址：ID 族 + 词典实体（资源名等，大小写不敏感整词匹配）
// 实体词典无权威来源前由调用方传 nil，仅保 ID 族；词典随分层检索（λ₁）建立后注入
func extractAddrs(text string, entities []string) []string {
	out := extractIDAddrs(text)
	seen := make(map[string]struct{}, len(out))
	for _, a := range out {
		seen[a] = struct{}{}
	}
	lower := strings.ToLower(text)
	for _, e := range entities {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if _, ok := seen[e]; ok {
			continue
		}
		if containsWholeWord(lower, strings.ToLower(e)) {
			seen[e] = struct{}{}
			out = append(out, e)
		}
	}
	return out
}

// 大小写不敏感整词匹配；入参须均已转小写
// 词典实体是资源名（词字符与连字符），词边界外的嵌入不算命中
func containsWholeWord(lowerText, lowerEntity string) bool {
	re, err := regexp.Compile(`\b` + regexp.QuoteMeta(lowerEntity) + `\b`)
	if err != nil {
		return false
	}
	return re.MatchString(lowerText)
}

// 原段有而压缩段没有的地址，保持原段出现序
func missingAddrs(original, compressed string, entities []string) []string {
	have := make(map[string]struct{})
	for _, a := range extractAddrs(compressed, entities) {
		have[a] = struct{}{}
	}
	missing := make([]string, 0)
	for _, a := range extractAddrs(original, entities) {
		if _, ok := have[a]; !ok {
			missing = append(missing, a)
		}
	}
	return missing
}

// C1 出口校验：压缩段缺地址则以 [addr_refs] 行补附，已覆盖时原样返回
// 幂等：footer 行内的地址参与后续抽取，重复调用不重复追加
func ensureAddrCoverage(original, compressed string, entities []string) string {
	missing := missingAddrs(original, compressed, entities)
	if len(missing) == 0 {
		return compressed
	}
	refs := addrRefsPrefix + strings.Join(missing, ", ")
	if strings.TrimSpace(compressed) == "" {
		return refs
	}
	return compressed + "\n" + refs
}

// 汇集历史段的地址源：正文连拼，RunID 字段一并纳入
// RunID 是结构性锚点（折叠骨架靠它寻址），摘要产物不得将其丢弃
func histAddrSource(hist []towerHistMsg) string {
	parts := make([]string, 0, len(hist)*2)
	for _, m := range hist {
		parts = append(parts, m.Content)
		if id := strings.TrimSpace(m.RunID); id != "" {
			parts = append(parts, id)
		}
	}
	return strings.Join(parts, "\n")
}
