// 会话列表子命令：列出数据目录下的历史会话（会话发现）。
//
// 纯读取命令：不校验大模型配置（口径同 judge），只开会话存储。
// 表格为首问截断的终端视图（截断是展示层行为，权威数据全量留在盘上）；
// json 输出全文字段供机器消费，不设条数上限（#18）。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"
	"unicode/utf8"

	"github.com/Aruing/Aruing/internal/config"
	"github.com/Aruing/Aruing/internal/session"
	"github.com/Aruing/Aruing/internal/store"
)

// 表格视图的首问预览长度（runes）；超长截断加省略号，仅影响展示
const sessionsPreviewRunes = 48

// 表格视图的时间格式（本地时区，分钟粒度）
const sessionsTimeLayout = "2006-01-02 15:04"

// 解析会话列表子命令参数并输出会话汇总。
// 标准错误承载生效数据目录与坏会话警告；标准输出只承载列表结果
func runSessions(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("sessions", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "path to config YAML (or ARUING_CONFIG / search paths)")
	dataDir := fs.String("data-dir", "", "data directory for session persistence (overrides storage.data_dir / default ~/.aruing/data)")
	format := fs.String("format", "table", "output format: table|json")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: aruing sessions [flags]")
		fmt.Fprintln(stderr, "")
		fmt.Fprintln(stderr, "List saved sessions (pure read, no LLM required).")
		fmt.Fprintln(stderr, "")
		fmt.Fprintln(stderr, "Flags:")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return errors.New("sessions takes no positional arguments; resume one with: aruing chat --session <id>")
	}
	switch *format {
	case "table", "json":
	default:
		return fmt.Errorf("unknown format %q: use table or json", *format)
	}

	cfg, _, err := config.LoadResolvedNoLLM(*configPath)
	if err != nil {
		return err
	}
	// 显式数据目录优先（同 run/chat 覆盖模式）；空则沿用加载链默认
	if d := strings.TrimSpace(*dataDir); d != "" {
		cfg.Storage.DataDir = d
	}
	dir := cfg.Storage.DataDir

	fmt.Fprintf(stderr, "data dir: %s\n", dir)

	// 列表只需会话存储：不开诊断账本 / 挂起快照 / 留存盘
	st, err := store.NewDiskStore(context.Background(), dir)
	if err != nil {
		return fmt.Errorf("open sessions: %w", err)
	}
	// 退出前归还磁盘句柄（P2-3 生命周期收口）
	defer closeStores(st)

	summaries, err := st.ListSessions(context.Background())
	if err != nil {
		return err
	}
	if len(summaries) == 0 {
		fmt.Fprintf(stdout, "no sessions found (data dir: %s)\n", dir)
		fmt.Fprintln(stdout, "start one with: aruing chat")
		return nil
	}
	if *format == "json" {
		return writeSessionsJSON(stdout, summaries)
	}
	writeSessionsTable(stdout, summaries)
	return nil
}

// 表格视图：tabwriter 对齐五列（编号 / 创建 / 最近活跃 / 消息数 / 首问预览）
func writeSessionsTable(w io.Writer, summaries []session.SessionSummary) {
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "SESSION\tCREATED\tLAST ACTIVE\tMSGS\tFIRST QUESTION")
	for _, s := range summaries {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\n",
			s.ID,
			s.CreatedAt.Local().Format(sessionsTimeLayout),
			s.UpdatedAt.Local().Format(sessionsTimeLayout),
			s.MessageCount,
			questionPreview(s.FirstQuestion),
		)
	}
	_ = tw.Flush()
}

// json 视图的行形状：snake_case 字段，时间 RFC3339，首问全文不截断
type sessionJSONRow struct {
	ID            string `json:"id"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
	MessageCount  int    `json:"message_count"`
	FirstQuestion string `json:"first_question"`
}

// json 视图：机器消费无终端宽度约束，首问输出全文
func writeSessionsJSON(w io.Writer, summaries []session.SessionSummary) error {
	rows := make([]sessionJSONRow, 0, len(summaries))
	for _, s := range summaries {
		rows = append(rows, sessionJSONRow{
			ID:            s.ID,
			CreatedAt:     s.CreatedAt.Format(time.RFC3339),
			UpdatedAt:     s.UpdatedAt.Format(time.RFC3339),
			MessageCount:  s.MessageCount,
			FirstQuestion: s.FirstQuestion,
		})
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(rows)
}

// 首问单行预览：折行与连续空白折叠为单空格，超长截断加省略号。
// 仅展示层行为，不动盘上权威数据（#18 管留存不管终端宽度）；
// 无首问的会话以短横线占位
func questionPreview(q string) string {
	if q == "" {
		return "-"
	}
	folded := strings.Join(strings.Fields(q), " ")
	if utf8.RuneCountInString(folded) <= sessionsPreviewRunes {
		return folded
	}
	runes := []rune(folded)
	return string(runes[:sessionsPreviewRunes]) + "…"
}
