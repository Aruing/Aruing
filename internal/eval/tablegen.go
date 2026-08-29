// 参数化大表生成器：表格投影对比实验的数据源（纯函数、固定种子、逐字节可复现）
//
// 生成 kubectl get pods 形态的表：NAME / READY / STATUS / RESTARTS / AGE / NODE
// 根因行（CrashLoopBackOff）固定在指定位；周围混无害扰动（少量 Pending、稀有节点、
// 个别高重启但 Running 的行），让覆盖目标与异常目标都有非平凡论域
// 不依赖集群与模型——投影是纯函数，机械判分主实验零成本循环

package eval

import (
	"fmt"
	"math/rand"
)

// TableSpec 大表生成参数
type TableSpec struct {
	// 总行数 N（含根因行）
	Rows int
	// 根因行的 0 基行号；必须在 [0, Rows) 内
	RootRow int
	// 随机种子；同参数同种子生成逐字节相同的表
	Seed int64
}

// GeneratedTable 生成结果
type GeneratedTable struct {
	// 列名（kubectl get pods 形态）
	Columns []string
	// 数据行
	Rows [][]string
	// 根因 pod 名（判分真值的资源名）
	RootName string
}

// 表内固定分布常数：无害扰动的注入密度
// 取值让 STATUS / READY / RESTARTS / NODE 都是「偏斜但非清一色」的显著列，
// NAME / AGE 高基数自动排除（与真实 kubectl 输出的列结构一致）
const (
	pendingRowsPer100  = 2 // 无害 Pending 行密度（每 100 行 2 个）
	rareNodeRowsPer100 = 3 // 稀有节点行密度
	rareNodeName       = "node-9"
	rootRestarts       = 7
)

// GenerateTable 按参数生成一张大表
// RootRow 越界返回错误：位置是实验变量（头/中/尾分桶），静默钳位会毁掉分桶口径
func GenerateTable(spec TableSpec) (GeneratedTable, error) {
	if spec.Rows <= 0 {
		return GeneratedTable{}, fmt.Errorf("tablegen: Rows must be positive, got %d", spec.Rows)
	}
	if spec.RootRow < 0 || spec.RootRow >= spec.Rows {
		return GeneratedTable{}, fmt.Errorf("tablegen: RootRow %d out of range [0, %d)", spec.RootRow, spec.Rows)
	}

	rng := rand.New(rand.NewSource(spec.Seed))
	rootName := fmt.Sprintf("bad-deploy-%06d", rng.Intn(1000000))

	columns := []string{"NAME", "READY", "STATUS", "RESTARTS", "AGE", "NODE"}
	rows := make([][]string, spec.Rows)
	for i := 0; i < spec.Rows; i++ {
		if i == spec.RootRow {
			rows[i] = []string{rootName, "0/1", "CrashLoopBackOff", fmt.Sprintf("%d", rootRestarts), "5m", "node-1"}
			continue
		}
		name := fmt.Sprintf("work-%05d-%04x", i, rng.Intn(0x10000))
		ready := "1/1"
		status := "Running"
		restarts := "0"
		// 无害扰动：Pending 少量混入（未就绪但非故障）
		if rng.Intn(100) < pendingRowsPer100 {
			ready = "0/1"
			status = "Pending"
		}
		// 个别高重启但 Running：单列统计会误报的行，逼异常检测用多元结构
		if rng.Intn(100) == 0 {
			restarts = fmt.Sprintf("%d", 3+rng.Intn(4))
		}
		node := fmt.Sprintf("node-%d", 1+rng.Intn(3))
		// 稀有节点少量混入：覆盖论域的非平凡来源
		if rng.Intn(100) < rareNodeRowsPer100 {
			node = rareNodeName
		}
		age := fmt.Sprintf("%dd", 1+rng.Intn(30))
		rows[i] = []string{name, ready, status, restarts, age, node}
	}
	return GeneratedTable{Columns: columns, Rows: rows, RootName: rootName}, nil
}
