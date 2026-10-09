package k8s

// 盘上延伸切片：对带 spool 引用的超巨观察，从完整 stdout 只读流式切页，
// 关闭内存内联截断点之后的不可达（#18）。行级模式（Columns=nil），行号 =
// 全量 stdout 物理行号（0 基），与截断点前的内联切片同一坐标系；
// 带时间窗时行首 RFC3339 流式过滤再开窗（复用 timecursor 行首解析；
// 窗内 total 需全扫，O(file) 低频可接受）；引用带 TotalLines 时非时间窗
// 路径取满窗口即止读（捕获侧已计数，翻页 total 免二次全扫）。
// 纯机械投影，不解释行内容（#16/#19）；Raw 与盘上文件均只读不回写。

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Aruing/Aruing/internal/tools"
	"github.com/Aruing/Aruing/internal/tools/summary"
)

// 从本工具 Raw 与盘上全量流切页；raw 须为本工具写入的 resultRaw JSON（含引用统计）
func (t *Tool) SliceSpool(raw []byte, q tools.SliceQuery, spool io.Reader) (tools.SliceView, error) {
	if spool == nil {
		return tools.SliceView{}, errors.New("spool reader is nil")
	}
	if len(raw) == 0 {
		return tools.SliceView{}, errors.New("empty raw")
	}
	var rr resultRaw
	if err := json.Unmarshal(raw, &rr); err != nil {
		return tools.SliceView{}, fmt.Errorf("decode k8s raw: %w", err)
	}
	if rr.ExitCode != 0 {
		return tools.SliceView{}, fmt.Errorf("kubectl exitCode=%d, not sliceable", rr.ExitCode)
	}

	offset, limit := clampSliceWindow(q.Offset, q.Limit)
	windowed := q.Since != "" || q.Until != ""
	var sinceT, untilT time.Time
	if windowed {
		var err error
		if sinceT, untilT, err = parseTimeWindow(q.Since, q.Until); err != nil {
			return tools.SliceView{}, err
		}
	}

	// 引用统计可用且无时间窗时：total 直接取捕获侧计数，取满窗口即止读
	refTotal := 0
	if rr.StdoutSpool != nil && rr.StdoutSpool.TotalLines > 0 {
		refTotal = rr.StdoutSpool.TotalLines
	}
	earlyExit := !windowed && refTotal > 0

	reader := bufio.NewReader(spool)
	var (
		rows        [][]string
		lineNo      int // 无时间窗 = 全量物理行号；有时间窗 = 窗内保留行序号（均 0 基）
		first, last string
	)
	for {
		line, readErr := reader.ReadString('\n')
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return tools.SliceView{}, fmt.Errorf("read spool: %w", readErr)
		}
		if content, isLine := physicalLine(line); isLine {
			kept := true
			if windowed {
				ts, parsed := leadingTimestamp(content)
				if !parsed {
					// 混杂无时间戳行：整体失败并引导（与内联过滤同语义，#18 不静默丢行）
					return tools.SliceView{}, errors.New(timeCursorHint)
				}
				if (q.Since != "" && ts.Before(sinceT)) || (q.Until != "" && ts.After(untilT)) {
					kept = false
				} else {
					if first == "" {
						first = ts.Format(time.RFC3339Nano)
					}
					last = ts.Format(time.RFC3339Nano)
				}
			}
			if kept {
				if lineNo >= offset && len(rows) < limit {
					rows = append(rows, []string{content})
				}
				lineNo++
				if earlyExit && len(rows) == limit {
					break
				}
			}
		}
		if readErr != nil {
			break
		}
	}

	total := lineNo
	if !windowed && refTotal > 0 {
		total = refTotal
	}
	if offset > total {
		offset = total
	}
	return tools.SliceView{
		Total:       total,
		Offset:      offset,
		Limit:       limit,
		Rows:        rows,
		WindowFirst: first,
		WindowLast:  last,
	}, nil
}

// 物理行判定与内容归一：完整行（含换行）与末尾无换行的残留内容都是一行
// （与捕获侧 spoolCounter 同语义）；空串（EOF 无残留）不是行。
// 只剁一个 \n 终止符：行尾 \r 是内容不是终止符（与内联切片 Split("\n") 同语义，
// #19 投影不改写内容）
func physicalLine(s string) (string, bool) {
	if s == "" {
		return "", false
	}
	return strings.TrimSuffix(s, "\n"), true
}

// 查询窗口钳制：与 summary.SliceRows 同语义（offset 负归零；limit 非正取默认、超顶截顶）
func clampSliceWindow(offset, limit int) (int, int) {
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		limit = summary.DefaultSliceLimit
	}
	if limit > summary.MaxSliceLimit {
		limit = summary.MaxSliceLimit
	}
	return offset, limit
}
