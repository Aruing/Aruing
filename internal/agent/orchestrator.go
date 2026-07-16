// 编排模块负责按固定顺序连接问题理解、目标定位、规划、执行、判断和报告
//
// 编排器只依赖各角色对调用方暴露的最小能力，不读取角色内部状态
// 当前闭环按计划顺序串行执行任务，依赖图调度和持久化留到后续阶段
// 证据编号由编排边界统一生成，工具不能决定领域实体身份
package agent

import (
	"context"
	"errors"
	"fmt"
	"time"

	"aruing/internal/core"
)

// 描述编排器理解原始问题所需的最小能力
type parser interface {
	// 把运行数据转换为未验证的问题结构
	Parse(context.Context, core.Run) (core.Query, error)
}

// 描述编排器确认真实目标所需的最小能力
type resolver interface {
	// 根据问题结构返回已确认目标
	Resolve(context.Context, core.Query) ([]core.Target, error)
}

// 描述编排器生成猜想和任务所需的最小能力
type planner interface {
	// 根据问题结构和目标返回本轮计划
	Plan(context.Context, core.Query, []core.Target) (Plan, error)
}

// 描述编排器执行单个工具任务所需的最小能力
type taskExecutor interface {
	// 执行任务并返回尚未分配领域编号的证据
	Execute(context.Context, core.Task) (*core.Evidence, error)
}

// 描述编排器根据证据形成判断所需的最小能力
type verifier interface {
	// 根据猜想、任务和实际证据返回判断列表
	Verify(context.Context, []core.Hypothesis, []core.Task, []core.Evidence) ([]core.Verdict, error)
}

// 描述编排器生成最终用户输出所需的最小能力
type reporter interface {
	// 根据当前运行、判断和证据生成报告
	Report(context.Context, core.Run, []core.Verdict, []core.Evidence) (core.Report, error)
}

// 描述编排器创建动态领域实体所需的最小元数据能力
type entityFactory interface {
	// 根据开放前缀生成全局唯一编号
	NewID(string) (string, error)
	// 返回统一时区的当前时间
	Now() time.Time
}

// 保存完整假闭环所需的角色和执行依赖
// 实例只控制调用顺序，不承担任何角色内部的业务判断
type Orchestrator struct {
	// 负责把原始问题转换为结构化线索
	parser parser
	// 负责把线索转换为已确认目标
	resolver resolver
	// 负责生成候选猜想和工具任务
	planner planner
	// 负责调用工具并返回证据内容
	executor taskExecutor
	// 负责依据证据生成判断
	verifier verifier
	// 负责把判断整理为最终报告
	reporter reporter
	// 负责为工具返回的证据分配领域编号和创建时间
	factory entityFactory
}

// 绑定完整闭环所需依赖并创建编排器
// 依赖有效性在执行入口统一检查，创建过程不产生外部副作用
func NewOrchestrator(
	parserRole parser,
	resolverRole resolver,
	plannerRole planner,
	executor taskExecutor,
	verifierRole verifier,
	reporterRole reporter,
	factory entityFactory,
) *Orchestrator {
	return &Orchestrator{
		parser:   parserRole,
		resolver: resolverRole,
		planner:  plannerRole,
		executor: executor,
		verifier: verifierRole,
		reporter: reporterRole,
		factory:  factory,
	}
}

// 从一次运行开始依次推进全部角色，成功时返回最终报告
// 任一阶段失败都会立即停止并保留阶段上下文，不返回部分报告
func (o *Orchestrator) Execute(ctx context.Context, run core.Run) (core.Report, error) {
	if err := ctx.Err(); err != nil {
		return core.Report{}, fmt.Errorf("execute run: %w", err)
	}
	if err := o.validate(); err != nil {
		return core.Report{}, err
	}

	query, err := o.parser.Parse(ctx, run)
	if err != nil {
		return core.Report{}, fmt.Errorf("parse run: %w", err)
	}
	targets, err := o.resolver.Resolve(ctx, query)
	if err != nil {
		return core.Report{}, fmt.Errorf("resolve targets: %w", err)
	}
	plan, err := o.planner.Plan(ctx, query, targets)
	if err != nil {
		return core.Report{}, fmt.Errorf("plan tasks: %w", err)
	}

	// 当前按计划顺序执行，先保证证据链闭合，再单独设计任务依赖调度
	evidence := make([]core.Evidence, 0, len(plan.Tasks))
	for _, task := range plan.Tasks {
		evidenceID, idErr := o.factory.NewID("e")
		if idErr != nil {
			return core.Report{}, fmt.Errorf("create evidence ID for task %q: %w", task.ID, idErr)
		}
		if evidenceID == "" {
			return core.Report{}, fmt.Errorf("create evidence ID for task %q: ID is required", task.ID)
		}
		item, executeErr := o.executor.Execute(ctx, task)
		if executeErr != nil {
			return core.Report{}, fmt.Errorf("execute task %q: %w", task.ID, executeErr)
		}
		if item == nil {
			return core.Report{}, fmt.Errorf("execute task %q: evidence is required", task.ID)
		}
		item.ID = evidenceID
		item.CreatedAt = o.factory.Now()
		evidence = append(evidence, *item)
	}

	verdicts, err := o.verifier.Verify(ctx, plan.Hypotheses, plan.Tasks, evidence)
	if err != nil {
		return core.Report{}, fmt.Errorf("verify evidence: %w", err)
	}
	report, err := o.reporter.Report(ctx, run, verdicts, evidence)
	if err != nil {
		return core.Report{}, fmt.Errorf("build report: %w", err)
	}
	return report, nil
}

// 检查执行所需依赖是否完整，避免在流程中途出现空对象崩溃
func (o *Orchestrator) validate() error {
	if o == nil {
		return errors.New("orchestrator is required")
	}
	if o.parser == nil || o.resolver == nil || o.planner == nil {
		return errors.New("orchestrator requires parser, resolver, and planner")
	}
	if o.executor == nil || o.verifier == nil || o.reporter == nil {
		return errors.New("orchestrator requires executor, verifier, and reporter")
	}
	if o.factory == nil {
		return errors.New("orchestrator requires an entity factory")
	}
	return nil
}
