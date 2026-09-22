package tools

import "io"

// 可切片后端：从证据原始输出中机械切出一页（offset/limit），不解释取值含义
// 实现方解析本工具写入的 Raw 形态；无法解析或不支持时返回错误
type Slicer interface {
	// 按查询从 raw 切片；offset 为行起点（0 基），limit 为最多行数
	Slice(raw []byte, q SliceQuery) (SliceView, error)
}

// 盘上延伸切片器：观察 Raw 带盘上留存引用（StdoutSpoolRef 探测命中）时，
// 从完整 stdout 只读流切页，可翻到内存内联截断点之后（#18）
// raw 为观察原始 JSON，实现方自行解析自身形态与引用统计；实现方须流式扫描，
// 不得把盘上内容全量驻内存（#19 切片是机械投影）
type SpoolSlicer interface {
	// spool 为该观察 spool 文件打开后的完整 stdout 只读流；行号与 total 覆盖全量输出
	SliceSpool(raw []byte, q SliceQuery, spool io.Reader) (SliceView, error)
}

// 切片查询：行级窗口 + 可选时间窗
// 时间窗语义对所有后端一致：Since/Until 为 RFC3339 闭区间，至少一个非空时后端先在时间维过滤，
// 再在过滤结果集上按 Offset/Limit 开窗；后端无法按时间切时明确报错，不得静默丢行（#18）
type SliceQuery struct {
	// 起始行（含），0 基；负值按 0 处理
	Offset int
	// 最多返回行数；非正时由调用方或实现侧使用默认
	Limit int
	// 可选时间窗起点（含），RFC3339；空表示不限
	Since string
	// 可选时间窗终点（含），RFC3339；空表示不限
	Until string
}

// 切片结果：总行数与当前窗口
type SliceView struct {
	// 全表行数（未切片前）
	Total int
	// 本页起始行（含）
	Offset int
	// 本页行上限（请求的 limit，可能大于实际返回行数）
	Limit int
	// 列名；非表格时可为 nil
	Columns []string
	// 本页行（单元格字符串）
	Rows [][]string
	// 可选：带时间窗查询时窗内首行时间戳（RFC3339），后端支持时间过滤时回填；空表示未启用或无匹配
	WindowFirst string
	// 可选：带时间窗查询时窗内末行时间戳（RFC3339），后端支持时间过滤时回填；空表示未启用或无匹配
	WindowLast string
}
