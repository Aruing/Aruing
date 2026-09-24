// 参数化大表生成器：表格投影对比实验的数据源（纯函数、固定种子、逐字节可复现）
//
// 生成 kubectl get pods 形态的表：NAME / READY / STATUS / RESTARTS / AGE / NODE
// 根因行（CrashLoopBackOff）固定在指定位；周围混无害扰动（少量 Pending、稀有节点、
// 个别高重启但 Running 的行），让覆盖目标与异常目标都有非平凡论域
// 不依赖集群与模型——投影是纯函数，机械判分主实验零成本循环
//
// 拓展（本轮不做，并非最终形态）：①组合异常根因模式（全常见值、仅组合独特，钉「无单值
// 可 narrow」）；②fleet 连续块形态（同名前缀聚排，钉「片内无竞争者」）。见笔记
// requirements §7-6 与步骤 3 plan 非目标段；需时在此加 TableSpec 模式字段

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
	// 根因 fleet 密度（每 100 行的故障载体数）；0 = 唯一根因行模式（历史矩阵逐字节不变）
	// >0 时 CrashLoopBackOff 共 K=max(2, Rows×FleetPer100/100) 个载体（含根因行，均匀散布），
	// 且 RESTARTS / NODE 取值空间随 N 扩张：论域从个位数涨到数十，名额竞争（挤兑）随 N
	// 显现——钉单遍投影「中权值多载体根因被高权值单例挤出名额」的失效机理
	RootFleetPer100 int
}

// GeneratedTable 生成结果
type GeneratedTable struct {
	// 列名（kubectl get pods 形态）
	Columns []string
	// 数据行
	Rows [][]string
	// 根因 pod 名（判分真值的资源名）
	RootName string
	// 根因行的身份特征值（判分真值；生成器保证全表唯一归属——唯一模式下全表仅根因行
	// 持有，fleet 模式下仅故障载体行持有），供存在性判分与行展示判分分档使用
	RootFeatures []string
}

// 表内固定分布常数：无害扰动的注入密度
// 取值让 STATUS / READY / RESTARTS / NODE 都是「偏斜但非清一色」的显著列，
// NAME / AGE 高基数自动排除（与真实 kubectl 输出的列结构一致）
const (
	pendingRowsPer100  = 2 // 无害 Pending 行密度（每 100 行 2 个）
	rareNodeRowsPer100 = 3 // 稀有节点行密度
	rareNodeName       = "node-9"
	rootRestarts       = 7
	rootStatus         = "CrashLoopBackOff"
)

// fleet 模式论域扩张参数：非常规重启值与稀有节点按固定步长轮转注入，
// 计数随 N 线性增长而取值空间有上界（守 MaxDistinctForHist 直方图口径），
// 让「覆盖名额挤兑」随表规模显现：小表论域 < 名额全盖住，大表论域 > 名额中权值被挤出
const (
	fleetRestartsStride = 20 // 非常规重启行注入步长（每 20 行 1 行，值在 1..R 向轮转）
	fleetNodeStride     = 25 // 稀有节点行注入步长（node-4..node-G 轮转）
	fleetMaxRestarts    = 20 // 非常规重启取值上限（+0 共 21 ≤ MaxDistinctForHist）
	fleetMaxNodes       = 20 // 节点取值上限（含 3 个主流节点）
)

// fleetSize 根因载体总数：唯一模式 = 1；fleet 模式 K = Rows×RootFleetPer100/100，下限 2
func fleetSize(spec TableSpec) int {
	if spec.RootFleetPer100 <= 0 {
		return 1
	}
	k := spec.Rows * spec.RootFleetPer100 / 100
	if k < 2 {
		k = 2
	}
	return k
}

// richRestartValues fleet 模式非常规重启取值数 R：随 N 增长，上限 fleetMaxRestarts
func richRestartValues(n int) int {
	r := n / 100
	if r < 2 {
		r = 2
	}
	if r > fleetMaxRestarts {
		r = fleetMaxRestarts
	}
	return r
}

// richNodeValues fleet 模式节点取值总数 G：随 N 增长，上限 fleetMaxNodes（含 3 个主流）
func richNodeValues(n int) int {
	g := 3 + n/250
	if g > fleetMaxNodes {
		g = fleetMaxNodes
	}
	return g
}

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

	// fleet 模式预计算：故障载体行集合（根因行 + 均匀散布的 K-1 个同行）。
	// 唯一模式下集合只含根因行，后续行为与历史生成器逐字节一致（rng 消耗序不变）
	fleet := fleetSize(spec)
	crash := map[int]bool{spec.RootRow: true}
	if fleet > 1 {
		stride := spec.Rows / fleet
		if stride < 1 {
			stride = 1
		}
		for i := 0; len(crash) < fleet && i < spec.Rows; i += stride {
			crash[i] = true
		}
	}
	richR := richRestartValues(spec.Rows)
	richG := richNodeValues(spec.Rows)
	restartsVal := 0 // 非常规重启值轮转计数（确定性，不耗 rng）
	rareNodeVal := 0 // 稀有节点值轮转计数

	columns := []string{"NAME", "READY", "STATUS", "RESTARTS", "AGE", "NODE"}
	rows := make([][]string, spec.Rows)
	for i := 0; i < spec.Rows; i++ {
		// 唯一模式根因行：零 rng 消耗、字段全固定（与历史生成器逐字节一致）
		if fleet == 1 && i == spec.RootRow {
			rows[i] = []string{rootName, "0/1", rootStatus, fmt.Sprintf("%d", rootRestarts), "5m", "node-1"}
			continue
		}
		// 先按普通行生成（消耗 rng，保证模式间同种子同名）；fleet 行随后覆盖故障字段
		name := fmt.Sprintf("work-%05d-%04x", i, rng.Intn(0x10000))
		ready := "1/1"
		status := "Running"
		restarts := "0"
		// 无害扰动：Pending 少量混入（未就绪但非故障）
		if rng.Intn(100) < pendingRowsPer100 {
			ready = "0/1"
			status = "Pending"
		}
		if fleet > 1 {
			// 论域扩张：非常规重启值与稀有节点按步长轮转注入，计数与取值数随 N 增长
			if i%fleetRestartsStride == 0 {
				restartsVal = restartsVal%richR + 1
				restarts = fmt.Sprintf("%d", restartsVal)
			}
			node := fmt.Sprintf("node-%d", 1+rng.Intn(3))
			if richG > 3 && i%fleetNodeStride == 0 {
				rareNodeVal = 4 + rareNodeVal%(richG-3)
				node = fmt.Sprintf("node-%d", rareNodeVal)
			}
			if crash[i] {
				// 故障载体行：多载体中权值根因（CrashLoopBackOff×K），被高权单例挤出名额的受害者；
				// 根因行本身保留 bad-deploy 名（判分真值），其余载体行同名前缀聚簇不成立（散布）
				crashName := name
				if i == spec.RootRow {
					crashName = rootName
				}
				rows[i] = []string{crashName, "0/1", rootStatus, fmt.Sprintf("%d", rootRestarts), "5m", node}
				continue
			}
			age := fmt.Sprintf("%dd", 1+rng.Intn(30))
			rows[i] = []string{name, ready, status, restarts, age, node}
			continue
		}
		// 唯一模式：历史行为原样保留
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
	return GeneratedTable{Columns: columns, Rows: rows, RootName: rootName, RootFeatures: []string{rootStatus}}, nil
}
