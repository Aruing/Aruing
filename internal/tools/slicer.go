package tools

// 可切片后端：从证据原始输出中机械切出一页（offset/limit），不解释取值含义
// 实现方解析本工具写入的 Raw 形态；无法解析或不支持时返回错误
type Slicer interface {
	// 按查询从 raw 切片；offset 为行起点（0 基），limit 为最多行数
	Slice(raw []byte, q SliceQuery) (SliceView, error)
}

// 切片查询：行级窗口
type SliceQuery struct {
	// 起始行（含），0 基；负值按 0 处理
	Offset int
	// 最多返回行数；非正时由调用方或实现侧使用默认
	Limit int
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
}
