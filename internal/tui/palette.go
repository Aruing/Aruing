// 命令面板内核：从一行未提交输入推导 `/` 命令菜单（纯函数，零终端依赖）。
// inline 与 app 双模式共用同一推导，行为一致由构造保证（功能头完成标志 1）。
// 菜单与 /help 同源于 commandDocs——菜单即可用命令的权威参考，加命令只改一处。
// 按键捕获归各模式（app 在 Update 拦截；inline 在 readline Listener / 包装层），
// 本文件只做推导与文本变换。面板是 UI 语法，不进 agent 意图枚举、模型不可见
// （#2）；纯展示层，不碰 Store / RunLedger（#20）。
package tui

import "strings"

// 菜单视图：过滤后的有序命令与选中下标；items 为空 = 不显示
type paletteView struct {
	// 过滤后的命令，顺序即展示顺序（前缀命中在前、子序列命中在后，同层按声明序）
	items []commandDoc
	// 选中下标，由 moveSel 维护在 [0, len(items)) 内
	sel int
}

// filterPalette 从一行未提交输入推导菜单：
//   - 激活 = 行首（忽略前导空白）为 '/' 且整行无换行（命令是单行语法，多行草稿不激活）
//   - query = '/' 后至首个空白的片段，大小写不敏感
//   - 排序 = 前缀命中在前、子序列命中次之，同层按 commandDocs 声明序（稳定）
//   - 无命中返回零值（菜单隐藏，提交走既有未知命令错误路径，不新增提示分支）
func filterPalette(line string) paletteView {
	if strings.ContainsAny(line, "\n\r") {
		return paletteView{}
	}
	text := strings.TrimLeft(line, " \t")
	if !strings.HasPrefix(text, "/") {
		return paletteView{}
	}
	query := strings.ToLower(commandFragment(text))
	if query == "" {
		return paletteView{items: append([]commandDoc(nil), commandDocs...)}
	}
	var subseq []commandDoc
	items := make([]commandDoc, 0, len(commandDocs))
	for _, d := range commandDocs {
		name := strings.ToLower(d.name)
		switch {
		case strings.HasPrefix(name, query):
			items = append(items, d)
		case isSubsequence(query, name):
			subseq = append(subseq, d)
		}
	}
	items = append(items, subseq...)
	if len(items) == 0 {
		return paletteView{}
	}
	return paletteView{items: items}
}

// commandFragment 提取命令名片段：'/' 后至首个空白
func commandFragment(text string) string {
	rest := strings.TrimPrefix(text, "/")
	if i := strings.IndexAny(rest, " \t"); i >= 0 {
		return rest[:i]
	}
	return rest
}

// isSubsequence 子序列匹配（命令名为小写 ASCII，按字节比较足够）：
// query 的每个字符按序出现在 name 中（如 "xit" ⊆ "exit"）
func isSubsequence(query, name string) bool {
	i := 0
	for j := 0; j < len(name) && i < len(query); j++ {
		if name[j] == query[i] {
			i++
		}
	}
	return i == len(query)
}

// active 报告菜单是否应显示
func (v paletteView) active() bool { return len(v.items) > 0 }

// selectedDoc 返回当前选中命令；菜单隐藏或下标越界返回 false（防御调用方状态漂移）
func (v paletteView) selectedDoc() (commandDoc, bool) {
	if !v.active() || v.sel < 0 || v.sel >= len(v.items) {
		return commandDoc{}, false
	}
	return v.items[v.sel], true
}

// moveSel 移动选中并钳制在 [0, len(items))（不环绕——命令量小，回卷用 Esc 关闭即可）
func (v paletteView) moveSel(delta int) paletteView {
	if !v.active() {
		return v
	}
	v.sel += delta
	if v.sel < 0 {
		v.sel = 0
	}
	if v.sel > len(v.items)-1 {
		v.sel = len(v.items) - 1
	}
	return v
}

// completePaletteLine：Tab 补全——命令名片段替换为选中规范名，其余文本原样保留
// （前导空白与其后的参数部分）；需参命令（args 非空）且其后无参数时补尾空格。
// 光标位固定行尾（补全后继续输入参数或直接提交）；未激活 / 无选中返回 ok=false
func completePaletteLine(line string, v paletteView) (newLine string, cursor int, ok bool) {
	doc, ok := v.selectedDoc()
	if !ok {
		return "", 0, false
	}
	lead := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
	rest := line[len(lead):]
	tail := ""
	if i := strings.IndexAny(rest, " \t"); i >= 0 {
		tail = rest[i:]
	} else if doc.args != "" {
		tail = " "
	}
	completed := lead + "/" + doc.name + tail
	return completed, len([]rune(completed)), true
}

// paletteSubmit：Enter 钉死——输入无空白（裸 /xxx）时命令名片段替换为选中规范名
// 交既有 parseCommand（handled=true；缺参命令由既有用法错误引导补参）；
// 输入含空白（已在输参数）原样返回（false），完整命令 + 参数走既有提交路径
func paletteSubmit(line string, v paletteView) (execLine string, handled bool) {
	doc, ok := v.selectedDoc()
	if !ok {
		return line, false
	}
	if strings.ContainsAny(strings.TrimLeft(line, " \t"), " \t") {
		return line, false
	}
	lead := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
	return lead + "/" + doc.name, true
}

// paletteLine 菜单展示行；样式归各模式主题 token，本类型不含样式
type paletteLine struct {
	text     string
	selected bool
}

// paletteKeyHint 菜单底行键位自述（完成标志 2：键位集在菜单内可发现）
const paletteKeyHint = "↑↓ 选择 · Tab 补全 · Enter 执行 · Esc 关闭"

// paletteRows 生成菜单展示行：每行正文与 /help 同形（/name <args>  usage），
// 选中行带 ❯ 前缀、未选中行两空格对齐；末行固定键位自述
func paletteRows(v paletteView) []paletteLine {
	rows := make([]paletteLine, 0, len(v.items)+1)
	for i, d := range v.items {
		text := "/" + d.name
		if d.args != "" {
			text += " " + d.args
		}
		text += "  " + d.usage
		if i == v.sel {
			text = "❯ " + text
		} else {
			text = "  " + text
		}
		rows = append(rows, paletteLine{text: text, selected: i == v.sel})
	}
	rows = append(rows, paletteLine{text: paletteKeyHint})
	return rows
}
