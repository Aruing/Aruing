package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Aruing/Aruing/internal/session"
)

// 假会话发现：固定列表 + 可控错误，覆盖 /sessions、/session 的可用与失败路径
type fakeLister struct {
	summaries []session.SessionSummary
	err       error
}

func (f fakeLister) ListSessions(context.Context) ([]session.SessionSummary, error) {
	return f.summaries, f.err
}

// 解析规则：/ 开头即命令语法；未知命令与参数非法必须报错（调用方不得发送）
func TestParseCommand(t *testing.T) {
	cases := []struct {
		name     string
		line     string
		ok       bool
		wantName string
		wantArgs []string
		wantErr  bool
	}{
		{"已知无参", "/help", true, "help", nil, false},
		{"已知带参", "/session abc", true, "session", []string{"abc"}, false},
		{"前后空白容忍", "  /sessions \t", true, "sessions", nil, false},
		{"普通输入", "hello world", false, "", nil, false},
		{"空串", "", false, "", nil, false},
		{"未知命令", "/nope", true, "nope", nil, true},
		{"斜杠开头的普通词也是命令语法", "/etc/hosts 怎么写", true, "etc/hosts", []string{"怎么写"}, true},
		{"大小写不归一", "/Help", true, "Help", nil, true},
		{"裸斜杠", "/", true, "", nil, true},
		{"缺参数", "/session", true, "session", nil, true},
		{"多参数", "/session a b", true, "session", []string{"a", "b"}, true},
		{"无参命令带参", "/help extra", true, "help", []string{"extra"}, true},
		{"多行输入不作为命令", "/session a\nb", true, "", nil, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			name, args, ok, err := parseCommand(c.line)
			if ok != c.ok {
				t.Fatalf("ok = %v, want %v", ok, c.ok)
			}
			if name != c.wantName {
				t.Fatalf("name = %q, want %q", name, c.wantName)
			}
			if len(args) != len(c.wantArgs) {
				t.Fatalf("args = %v, want %v", args, c.wantArgs)
			}
			for i := range args {
				if args[i] != c.wantArgs[i] {
					t.Fatalf("args = %v, want %v", args, c.wantArgs)
				}
			}
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, c.wantErr)
			}
		})
	}
}

// /help 输出与命令表同源：每条注册命令都出现
func TestRunCommandHelp(t *testing.T) {
	res := runCommand(context.Background(), commandEnv{}, "help", nil)
	if res.quit || res.switchTo != "" {
		t.Fatalf("help should be pure display: %+v", res)
	}
	if len(res.lines) != 1 || res.lines[0].isError {
		t.Fatalf("lines = %+v", res.lines)
	}
	for _, d := range commandDocs {
		if !strings.Contains(res.lines[0].text, "/"+d.name) {
			t.Fatalf("help output missing /%s: %q", d.name, res.lines[0].text)
		}
	}
}

// /sessions：全量列出并标当前；lister 缺失或报错时明确降级
func TestRunCommandSessions(t *testing.T) {
	env := commandEnv{
		lister: fakeLister{summaries: []session.SessionSummary{
			{ID: "sess_a", MessageCount: 3, FirstQuestion: "查 redis"},
			{ID: "sess_b", MessageCount: 1, FirstQuestion: "为什么 demo-api 起不来"},
		}},
		currentSessionID: func() string { return "sess_b" },
	}
	res := runCommand(context.Background(), env, "sessions", nil)
	if len(res.lines) != 3 {
		t.Fatalf("lines = %+v", res.lines)
	}
	for _, ln := range res.lines[1:] {
		marked := strings.HasPrefix(ln.text, "*")
		isCurrent := strings.Contains(ln.text, "sess_b")
		if marked != isCurrent {
			t.Fatalf("current mark wrong: %q", ln.text)
		}
	}

	for name, env := range map[string]commandEnv{
		"缺能力":  {},
		"列举失败": {lister: fakeLister{err: errBoom}},
	} {
		t.Run(name, func(t *testing.T) {
			res := runCommand(context.Background(), env, "sessions", nil)
			if len(res.lines) != 1 || !res.lines[0].isError {
				t.Fatalf("lines = %+v", res.lines)
			}
		})
	}
}

// /session：命中切换、未命中报错留原会话
func TestRunCommandSession(t *testing.T) {
	env := commandEnv{
		lister:           fakeLister{summaries: []session.SessionSummary{{ID: "s2"}}},
		currentSessionID: func() string { return "s1" },
	}
	res := runCommand(context.Background(), env, "session", []string{"s2"})
	if res.switchTo != "s2" {
		t.Fatalf("switchTo = %q", res.switchTo)
	}

	res = runCommand(context.Background(), env, "session", []string{"nope"})
	if res.switchTo != "" || len(res.lines) != 1 || !res.lines[0].isError {
		t.Fatalf("miss should error and stay: %+v", res)
	}
	if !strings.Contains(res.lines[0].text, "不存在") {
		t.Fatalf("error text = %q", res.lines[0].text)
	}
}

// /new：走注入的新建路径并切换；失败明确报错
func TestRunCommandNew(t *testing.T) {
	env := commandEnv{newSession: func(context.Context) (*session.Session, error) {
		return &session.Session{ID: "s9"}, nil
	}}
	res := runCommand(context.Background(), env, "new", nil)
	if res.switchTo != "s9" || len(res.lines) != 1 || res.lines[0].isError {
		t.Fatalf("res = %+v", res)
	}

	env.newSession = func(context.Context) (*session.Session, error) {
		return nil, errBoom
	}
	res = runCommand(context.Background(), env, "new", nil)
	if res.switchTo != "" || len(res.lines) != 1 || !res.lines[0].isError {
		t.Fatalf("failure res = %+v", res)
	}
}

// /exit 只置退出位
func TestRunCommandExit(t *testing.T) {
	res := runCommand(context.Background(), commandEnv{}, "exit", nil)
	if !res.quit || len(res.lines) != 0 {
		t.Fatalf("res = %+v", res)
	}
}

// inline 分支：命令渲染留痕、普通输入不拦截、未知命令渲染错误（三者都不发送）
func TestRunInlineCommand(t *testing.T) {
	st := mustLoadStyles("dark")
	cases := []struct {
		name    string
		text    string
		handled bool
		want    string
	}{
		{"命令渲染", "/help", true, "命令："},
		{"未知命令渲染错误", "/nope", true, "未知命令"},
		{"普通输入不拦截", "你好", false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var out strings.Builder
			_, handled := runInlineCommand(context.Background(), &out, st, commandEnv{}, c.text)
			if handled != c.handled {
				t.Fatalf("handled = %v, want %v", handled, c.handled)
			}
			if c.handled && !strings.Contains(stripANSI(out.String()), c.want) {
				t.Fatalf("output = %q, want %q", stripANSI(out.String()), c.want)
			}
		})
	}
}

// app 模式 Enter 提交命令：不置 busy、不启动 Turn、不产生 user 视图
func TestUpdateEnterCommand(t *testing.T) {
	m := newTestModel()
	m.input.SetValue("/help")
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	um := updated.(Model)
	if um.busy {
		t.Fatal("command should not set busy")
	}
	if cmd != nil {
		t.Fatal("command should not start Turn cmd")
	}
	if len(um.messages) != 1 || um.messages[0].kind != "system" {
		t.Fatalf("messages = %+v", um.messages)
	}
	if got := um.input.Value(); got != "" {
		t.Fatalf("input should reset, got %q", got)
	}
}

// app 模式 /session 切换：本地会话编号生效（lister 注入 fake）；
// 视图历史同步清空，旧会话消息不得混入新会话视图（回归：显示与活跃会话一致）
func TestUpdateEnterCommandSwitch(t *testing.T) {
	m := newTestModel()
	m.sessionID = "s1"
	m.messages = []msgView{{kind: "user", text: "旧会话消息"}}
	m.lister = fakeLister{summaries: []session.SessionSummary{{ID: "s2"}}}
	m.input.SetValue("/session s2")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	um := updated.(Model)
	if um.sessionID != "s2" {
		t.Fatalf("sessionID = %q, want s2", um.sessionID)
	}
	if len(um.messages) != 1 || um.messages[0].kind != "system" {
		t.Fatalf("messages = %+v", um.messages)
	}
	if strings.Contains(um.messages[0].text, "旧会话消息") {
		t.Fatal("stale history must not survive a session switch")
	}
}

// app 模式未知命令：错误视图，绝不作为消息发送
func TestUpdateEnterCommandUnknown(t *testing.T) {
	m := newTestModel()
	m.input.SetValue("/nope")
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	um := updated.(Model)
	if um.busy || cmd != nil {
		t.Fatalf("unknown command must not start turn: busy=%v cmd=%v", um.busy, cmd)
	}
	if len(um.messages) != 1 || um.messages[0].kind != "error" {
		t.Fatalf("messages = %+v", um.messages)
	}
	if !strings.Contains(um.messages[0].text, "未知命令") {
		t.Fatalf("text = %q", um.messages[0].text)
	}
}

// app 模式 /exit：置退出并返回 Quit
func TestUpdateEnterCommandExit(t *testing.T) {
	m := newTestModel()
	m.input.SetValue("/exit")
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	um := updated.(Model)
	if !um.quit || cmd == nil {
		t.Fatalf("quit=%v cmd=%v", um.quit, cmd)
	}
}
