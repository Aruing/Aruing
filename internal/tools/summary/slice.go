package summary

// 行级切片：对已解析的 columns/rows 做 offset/limit 窗口，供 evidence.read 与后端 Slicer 共用
// 纯机械；不解释列名或取值

// DefaultSliceLimit 当 limit 非正时使用的默认页大小
const DefaultSliceLimit = 32

// MaxSliceLimit 单次切片硬顶，避免上下文爆炸
const MaxSliceLimit = 200

// SliceRows 对 rows 做行窗口；offset/limit 钳到合法范围
// 返回的 Rows 为原切片子区间（共享底层数组时调用方勿改写单元格）
func SliceRows(columns []string, rows [][]string, offset, limit int) (outColumns []string, outRows [][]string, total, outOffset, outLimit int) {
	total = len(rows)
	outColumns = columns

	if offset < 0 {
		offset = 0
	}
	if offset > total {
		offset = total
	}
	if limit <= 0 {
		limit = DefaultSliceLimit
	}
	if limit > MaxSliceLimit {
		limit = MaxSliceLimit
	}

	end := offset + limit
	if end > total {
		end = total
	}
	if offset < end {
		outRows = rows[offset:end]
	} else {
		outRows = [][]string{}
	}
	return outColumns, outRows, total, offset, limit
}
