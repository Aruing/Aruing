package k8s

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"aruing/internal/tools"
	"aruing/internal/tools/summary"
)

// 日志时间游标：对 kubectl logs --timestamps 产物（行首 RFC3339 前缀）做机械时间窗过滤
// 纯格式投影：只解析行首时间戳并比较区间，不解释行内容（#16/#19）；Raw 不可变，过滤结果是派生视图

// 时间窗过滤提示（引导模型改用可成功的路径，不静默丢行 #18）
const timeCursorHint = "该输出不可按时间窗切片（要求全部行带行首 RFC3339 时间戳，如 kubectl logs --timestamps）；" +
	"请用源工具重新查询：k8s logs 加 --timestamps 重取，或直接加 --since-time / --tail 收窄"

// 行首时间戳可解析则返回解析值；空行或无 RFC3339 前缀返回零值与 false
func leadingTimestamp(line string) (time.Time, bool) {
	// kubectl --timestamps 格式："<RFC3339> <日志体>"；无空格即无前缀
	sp := strings.IndexByte(line, ' ')
	if sp <= 0 {
		return time.Time{}, false
	}
	ts, err := time.Parse(time.RFC3339, line[:sp])
	if err != nil {
		return time.Time{}, false
	}
	return ts, true
}

// 时间窗过滤：全部行带可解析的行首 RFC3339 时间戳才过滤；任一行不可解析即整体失败
// since/until 为 RFC3339 闭区间，至少一个非空；返回过滤后行集与窗内首末时间戳
func filterTimeWindow(lines []string, since, until string) ([]string, string, string, error) {
	var sinceT, untilT time.Time
	var err error
	if since != "" {
		if sinceT, err = time.Parse(time.RFC3339, since); err != nil {
			return nil, "", "", fmt.Errorf("invalid since %q: %w", since, err)
		}
	}
	if until != "" {
		if untilT, err = time.Parse(time.RFC3339, until); err != nil {
			return nil, "", "", fmt.Errorf("invalid until %q: %w", until, err)
		}
	}

	first, last := "", ""
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		ts, ok := leadingTimestamp(line)
		if !ok {
			// 混杂无时间戳行：跳过该行等于静默丢信息，整体失败并引导（#18）
			return nil, "", "", errors.New(timeCursorHint)
		}
		if since != "" && ts.Before(sinceT) {
			continue
		}
		if until != "" && ts.After(untilT) {
			continue
		}
		if first == "" {
			first = ts.Format(time.RFC3339Nano)
		}
		last = ts.Format(time.RFC3339Nano)
		kept = append(kept, line)
	}
	return kept, first, last, nil
}

// 带时间窗的行级切片：先过滤再在过滤结果集上开窗；total/offset/limit 均相对过滤后行集
func sliceTimeWindow(stdout string, q tools.SliceQuery) (tools.SliceView, error) {
	lines := strings.Split(strings.TrimRight(stdout, "\r\n"), "\n")
	kept, first, last, err := filterTimeWindow(lines, q.Since, q.Until)
	if err != nil {
		return tools.SliceView{}, err
	}
	rows := make([][]string, len(kept))
	for i, line := range kept {
		rows[i] = []string{line}
	}
	cols, page, total, offset, limit := summary.SliceRows(nil, rows, q.Offset, q.Limit)
	return tools.SliceView{
		Total:       total,
		Offset:      offset,
		Limit:       limit,
		Columns:     cols,
		Rows:        page,
		WindowFirst: first,
		WindowLast:  last,
	}, nil
}
