// 运行时命令：chat 交互内 "/" 前缀命令的解析与执行内核。
// inline 与 app 双模式共用同一套解析与执行，行为一致由构造保证（功能头完成标志 1）。
// 纯展示层纪律（#20）：命令只读会话数据与本地 UI 状态，不产生 Turn、
// 不写 Store / RunLedger、不假装 Evidence；命令是 UI 语法，不进 agent
// 意图枚举、模型不可见（#2）。渲染样式由各模式用自己的主题 token 完成，
// 本文件不碰样式
package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/Aruing/Aruing/internal/session"
)

// 首问预览的最大 rune 数；超长以省略号收尾（仅展示层截断，Store 仍留全文）
const previewMaxRunes = 40

// 命令说明条目：/help 的渲染与 ParseCommand 的参数形状校验同源于此表，
// 新增命令只改这里与 runCommand 的执行分支
type commandDoc struct {
	// 规范名：小写、无斜杠（如 "session"），解析按精确匹配、不做前缀或大小写归一
	name string
	// 参数形状说明（空 = 不接受参数），如 "<id>"；当前只支持至多一个参数的形状
	args string
	// 参数可省略：args 非空但允许零参调用（如 /theme 无参显示当前值）
	optionalArgs bool
	// 一句话用途，进 /help 输出
	usage string
}

// 命令全集，顺序即 /help 展示顺序
var commandDocs = []commandDoc{
	{name: "help", usage: "显示命令表"},
	{name: "sessions", usage: "列出历史会话（* 为当前）"},
	{name: "session", args: "<id>", usage: "切换到指定会话"},
	{name: "new", usage: "新建会话并切换（原会话可 /session 切回）"},
	{name: "clear", usage: "清屏（只清展示，会话数据仍全量保留）"},
	{name: "theme", args: "[dark|light|auto]", optionalArgs: true, usage: "切换主题（仅本进程；持久化改 tui.theme）"},
	{name: "exit", usage: "退出 chat（同 exit/quit）"},
}

// parseCommand 判定一行已提交输入是否命令语法：TrimSpace 后整条文本以 "/"
// 开头才算（含多行输入）。非命令语法 ok=false，调用方按普通消息走 Turn；
// 命令语法下已知命令返回 name 与参数，未知命令或参数非法返回 err——
// 调用方必须把错误呈现给用户，不得当作聊天消息发送（完成标志 1）
func parseCommand(line string) (name string, args []string, ok bool, err error) {
	text := strings.TrimSpace(line)
	if !strings.HasPrefix(text, "/") {
		return "", nil, false, nil
	}
	// 命令是单行语法：多行输入（shift+enter 续行等）一律按用法错误处理，不给隐义
	if strings.ContainsAny(text, "\n\r") {
		return "", nil, true, fmt.Errorf("命令是单行语法，多行输入不作为命令（/help 查看命令表）")
	}
	fields := strings.Fields(text)
	name = strings.TrimPrefix(fields[0], "/")
	args = fields[1:]
	doc, known := lookupCommand(name)
	if !known {
		return name, args, true, fmt.Errorf("未知命令 /%s（/help 查看命令表；以 / 开头按命令解析，不作为消息发送）", name)
	}
	if doc.args == "" && len(args) > 0 {
		return name, args, true, fmt.Errorf("/%s 不接受参数（/help 查看用法）", name)
	}
	// 参数形状只有「至多一参」一种：多参一律用法错误（可选参数命令同样不放开）
	if len(args) > 1 {
		return name, args, true, fmt.Errorf("/%s 用法：/%s %s", name, name, doc.args)
	}
	// 必参命令缺参走同一用法错误；可选参数命令允许零参（由执行分支决定零参语义）
	if doc.args != "" && len(args) == 0 && !doc.optionalArgs {
		return name, args, true, fmt.Errorf("/%s 用法：/%s %s", name, name, doc.args)
	}
	return name, args, true, nil
}

// 按规范名查命令说明；未注册返回 false
func lookupCommand(name string) (commandDoc, bool) {
	for _, d := range commandDocs {
		if d.name == name {
			return d, true
		}
	}
	return commandDoc{}, false
}

// 命令执行的能力注入：由模式装配（闭包读本地状态），测试给 fake。
// lister 为 nil 时仅会话类命令报不可用，help / new / exit 不受影响
type commandEnv struct {
	// 会话发现能力（/sessions、/session 校验共用一次列表语义）；可空
	lister session.SessionLister
	// 新建会话走既有 Service.NewSession 产品路径，不另起后门；可空（仅测试）
	newSession func(ctx context.Context) (*session.Session, error)
	// 返回当前会话编号（闭包读模式本地状态，切换后即时生效）
	currentSessionID func() string
	// 返回当前主题配置值（空 = 由终端背景自动选）；nil 时无参 /theme 不报错、只列候选
	currentTheme func() string
}

// 命令输出的一行：isError 决定各模式用错误样式渲染；文本自足（含提示语）
type renderLine struct {
	text    string
	isError bool
}

// 命令执行效果：quit 与 switchTo 由调用方应用到各自模式的本地状态，
// lines 为待渲染输出（含错误行），命令本身零业务写入
type commandResult struct {
	// /exit 置位：调用方以各自模式的方式退出
	quit bool
	// 非空 = 切换到该会话（仅改本地编号，Turn 每轮带编号，无需重载历史）
	switchTo string
	// /clear 置位：调用方擦除本模式的展示层（Store / RunLedger 不动）
	clearDisplay bool
	// 非空 = 目标主题名（已过白名单）：调用方重载样式与渲染器后生效
	theme string
	// 待渲染输出行，顺序即展示顺序
	lines []renderLine
}

// 单行错误结果的便捷构造
func errResult(text string) commandResult {
	return commandResult{lines: []renderLine{{text: text, isError: true}}}
}

// runCommand 执行一条已通过 ParseCommand 校验的命令。
// 只做读取与本地效果，不做任何会话写入；name 未注册时保守返回错误
// （正常流程不可达，防御内部误用）
func runCommand(ctx context.Context, env commandEnv, name string, args []string) commandResult {
	switch name {
	case "help":
		// 与 commandDocs 同源：命令表永远是全量且与解析一致
		var b strings.Builder
		b.WriteString("命令：")
		for _, d := range commandDocs {
			b.WriteString("\n/")
			b.WriteString(d.name)
			if d.args != "" {
				b.WriteString(" " + d.args)
			}
			b.WriteString("  " + d.usage)
		}
		// 面板键位自述与菜单底行同源：命令表与键位在 /help 一处可查（完成标志 2）
		b.WriteString("\n" + paletteKeyHint)
		return commandResult{lines: []renderLine{{text: b.String()}}}
	case "sessions":
		return listSessions(ctx, env)
	case "session":
		// parseCommand 已保证恰好一参；此处防御内部直呼时的越界，不信任调用方形状
		if len(args) != 1 {
			return errResult("/session 用法：/session <id>")
		}
		return switchSession(ctx, env, args[0])
	case "new":
		return newSessionCmd(ctx, env)
	case "clear":
		return clearCmd()
	case "theme":
		return themeCmd(env, args)
	case "exit":
		return commandResult{quit: true}
	}
	return errResult("未知命令 /" + name)
}

// /sessions：全量列出会话汇总，当前会话标 *；不分页（会话数量级小，
// 触顶再另立步骤谈分页）
func listSessions(ctx context.Context, env commandEnv) commandResult {
	if env.lister == nil {
		return errResult("会话列表不可用（当前存储未提供会话发现能力）")
	}
	summaries, err := env.lister.ListSessions(ctx)
	if err != nil {
		return errResult("列出会话失败：" + err.Error())
	}
	lines := []renderLine{{text: fmt.Sprintf("共 %d 个会话（* 为当前）：", len(summaries))}}
	for _, s := range summaries {
		mark := " "
		if s.ID == env.currentSessionID() {
			mark = "*"
		}
		lines = append(lines, renderLine{text: fmt.Sprintf("%s %s  %d 条消息  %s  %s",
			mark, s.ID, s.MessageCount, s.UpdatedAt.Format("2006-01-02 15:04"), firstQuestionPreview(s))})
	}
	return commandResult{lines: lines}
}

// /session <id>：列表成员校验后仅切换本地编号；未命中报错并留在原会话，
// 避免拼错编号拖到首轮 Turn 才失败
func switchSession(ctx context.Context, env commandEnv, target string) commandResult {
	if env.lister == nil {
		return errResult("会话切换不可用（当前存储未提供会话发现能力）")
	}
	summaries, err := env.lister.ListSessions(ctx)
	if err != nil {
		return errResult("列出会话失败：" + err.Error())
	}
	for _, s := range summaries {
		if s.ID == target {
			return commandResult{switchTo: target, lines: []renderLine{{text: "已切换到会话 " + target}}}
		}
	}
	return errResult(fmt.Sprintf("会话 %s 不存在（/sessions 查看列表；未切换）", target))
}

// /clear：只置展示层擦除效果，不触碰存储。确认行自带恢复出口，
// 避免用户以为数据被删（权威数据仍全量保留，#18）
func clearCmd() commandResult {
	return commandResult{
		clearDisplay: true,
		lines:        []renderLine{{text: "已清屏（会话数据全量保留：/sessions 查看，chat --session 续聊）"}},
	}
}

// /theme：无参显示当前配置值与候选（可发现性）；有参在白名单内则置切换效果，
// 不在白名单只报用法错误、不置 theme（模式层据此保持原样式）。
// 白名单在此收口而非交给样式层：非法主题名不该走到 loadStyles 的失败路径
func themeCmd(env commandEnv, args []string) commandResult {
	if len(args) == 0 {
		current := "auto"
		if env.currentTheme != nil {
			if v := strings.TrimSpace(env.currentTheme()); v != "" {
				current = v
			}
		}
		return commandResult{lines: []renderLine{{text: "当前主题：" + current + "（可 /theme dark|light|auto；持久化改 tui.theme）"}}}
	}
	switch args[0] {
	case "dark", "light", "auto":
		return commandResult{
			theme: args[0],
			lines: []renderLine{{text: "已切换主题 " + args[0] + "（仅本进程；持久化改 config tui.theme）"}},
		}
	}
	return errResult("/theme 用法：/theme [dark|light|auto]")
}

// /new：经 Service.NewSession 新建并切换；原会话留存可切回，不丢数据
func newSessionCmd(ctx context.Context, env commandEnv) commandResult {
	if env.newSession == nil {
		return errResult("新建会话不可用")
	}
	sess, err := env.newSession(ctx)
	if err != nil {
		return errResult("新建会话失败：" + err.Error())
	}
	return commandResult{switchTo: sess.ID, lines: []renderLine{{text: "已新建会话 " + sess.ID + "（原会话可用 /session 切回）"}}}
}

// 首问展示预览：单行化并按 rune 截断，仅供列表可读；Store 携带全文，
// 截断只发生在展示层
func firstQuestionPreview(s session.SessionSummary) string {
	if s.FirstQuestion == "" {
		return "（无提问）"
	}
	oneLine := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		return r
	}, s.FirstQuestion)
	runes := []rune(oneLine)
	if len(runes) > previewMaxRunes {
		return string(runes[:previewMaxRunes]) + "…"
	}
	return oneLine
}
