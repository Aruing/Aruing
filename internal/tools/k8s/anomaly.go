// 大表异常检测：one-hot 编码低基数列后做 PCA，在主成分空间用 Hotelling's T² 度量每行离群度
//
// 算法依据：
//   - PCA（Pearson 1901 / Hotelling 1933）：降维保留最大方差方向，即最大「分散」方向
//   - Hotelling's T²（Hotelling 1947）：多变量统计过程控制的标准异常度量，
//     T² = Σ (x_k - mean_k)² / var_k，相当于考虑方差归一化的马氏距离平方
//   - one-hot + PCA ≈ MCA（Benzécri 1973）的简化形态：跳过卡方距离标准化，保留核心
//
// 不引入业务语义：one-hot 是机械编码、PCA 是线性代数、T² 是统计度量。
// 不识别列名、不解释取值含义（守住 #16/#19）。

package k8s

import (
	"math"

	"gonum.org/v1/gonum/mat"
	"gonum.org/v1/gonum/stat"
)

// 异常检测的稳定性阈值：低于此行数时 PCA 与协方差都不可靠，调用方应走兜底
const minRowsForAnomaly = 30

// 最大主成分数：T² 在前几个高方差维度上就够分辨异常，更多维度反而引入噪声
const maxPCADims = 8

// T² 平滑项：方差为 0 的主成分会被剔除；若全部方差为 0 则返回 nil 触发兜底
//
// anomalyScores 返回与 rows 等长的切片；元素越大该行越异常
// 失败（行数不足、方差为零、协方差奇异）时返回 nil，调用方走纯分层抽样兜底
func anomalyScores(rows [][]string, sigCols []int, hists []map[string]int) []float64 {
	if len(rows) < minRowsForAnomaly || len(sigCols) == 0 {
		return nil
	}

	X := encodeOneHot(rows, sigCols, hists)
	if X == nil {
		return nil
	}
	n, d := X.Dims()
	if d == 0 || n <= 1 {
		return nil
	}

	// PCA：算主成分向量与各方差
	var pc stat.PC
	if !pc.PrincipalComponents(X, nil) {
		return nil
	}
	var vars []float64
	vars = pc.VarsTo(vars)
	if len(vars) == 0 {
		return nil
	}

	// 选前 k 个方差 > 0 的主成分；剔除零方差（恒定列）方向避免除零
	k := minInt(len(vars), maxPCADims)
	for k > 0 && vars[k-1] <= 1e-12 {
		k--
	}
	if k == 0 {
		return nil
	}

	// 主成分向量矩阵 V ∈ R^{d × min(n,d)}；VectorsTo 在 dst 为空时自动 resize
	// 取前 k 列（高方差方向）作为投影基底
	var Vfull mat.Dense
	pc.VectorsTo(&Vfull)
	_, pcols := Vfull.Dims()
	k = minInt(k, pcols)
	V := mat.NewDense(d, k, nil)
	for j := 0; j < k; j++ {
		for i := 0; i < d; i++ {
			V.Set(i, j, Vfull.At(i, j))
		}
	}
	proj := mat.NewDense(n, k, nil)
	proj.Mul(X, V)

	// 主成分空间下协方差对角化，方差就是 vars[:k]；T² = Σ_j (proj_ij - mean_j)² / var_j
	means := make([]float64, k)
	for j := 0; j < k; j++ {
		var sum float64
		for i := 0; i < n; i++ {
			sum += proj.At(i, j)
		}
		means[j] = sum / float64(n)
	}
	scores := make([]float64, n)
	for i := 0; i < n; i++ {
		var t2 float64
		for j := 0; j < k; j++ {
			diff := proj.At(i, j) - means[j]
			t2 += diff * diff / vars[j]
		}
		scores[i] = math.Sqrt(t2)
	}
	return scores
}

// one-hot 编码：低基数列 sigCols 每个取值生成一列；该行该列取此值则 1 否则 0
// 空串视为单独取值（与频次段一致）；返回 nil 表示编码失败（如所有列都无取值）
func encodeOneHot(rows [][]string, sigCols []int, hists []map[string]int) *mat.Dense {
	// 计算总列数 D 并为每个 (col, value) 分配列索引
	type colKey struct {
		colIdx int
		val    string
	}
	colMap := make(map[colKey]int, 0)
	for _, c := range sigCols {
		if c >= len(hists) {
			continue
		}
		for v := range hists[c] {
			colMap[colKey{c, v}] = len(colMap)
		}
	}
	D := len(colMap)
	if D == 0 {
		return nil
	}
	N := len(rows)
	flat := make([]float64, N*D)
	for i, r := range rows {
		for _, c := range sigCols {
			if c >= len(r) {
				continue
			}
			if j, ok := colMap[colKey{c, r[c]}]; ok {
				flat[i*D+j] = 1
			}
		}
	}
	return mat.NewDense(N, D, flat)
}

// 选「区分性列」索引：低基数（distinct ≤ maxDistinctForHist）且非清一色（dominant/N < 0.999）
// 高基数列（如 NAME 100 distinct）对异常检测无意义——每行都独特，无「少数派」概念
func significantColumns(hists []map[string]int) []int {
	var sig []int
	for i := range hists {
		h := hists[i]
		if len(h) == 0 || len(h) > maxDistinctForHist {
			continue
		}
		var max int
		var total int
		for _, c := range h {
			total += c
			if c > max {
				max = c
			}
		}
		if total == 0 {
			continue
		}
		if float64(max)/float64(total) >= 0.999 {
			continue
		}
		sig = append(sig, i)
	}
	return sig
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
