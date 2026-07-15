---
name: aruing-test-guidelines
description: Use when adding, modifying, reviewing, or refactoring tests in this repository, including test cases, assertions, fixtures, mocks, helper functions, and shared test utilities
---

# Test Guidelines

## 作用范围

在当前任务要求新增、修改、重构或审查测试时使用。本 skill 约束测试代码、测试辅助函数、fixture、mock、断言方式和测试命名，不要求扩大到无关测试文件

如果同时使用语言或框架测试 skill，本 skill 负责收住项目测试风格；具体语法、测试库 API 和运行命令按对应语言或框架处理

## 核心原则

测试用于证明关键行为成立，不追求完完全全覆盖所有内部路径和边角排列。优先覆盖正常功能、明确边界、已知回归和调用方真实依赖的行为

测试代码本身也要可维护。发现相同 setup、fixture、断言或 mock 逻辑在多个测试中反复出现时，优先提取清晰的小工具函数或共享 fixture，而不是复制粘贴到每个测试里

## 用例范围

测试用例保持克制，通常覆盖这些情况就够了：

- 主要正常路径：目标功能在典型输入下按预期工作
- 明确边界：空值、缺失参数、最小值、最大值、重复值、权限边界等真实会遇到的边界
- 明确错误：接口契约、校验规则、回滚行为或已知 bug 要求的错误路径
- 关键集成点：持久化、外部依赖、并发或状态变更中最能证明行为成立的观察点

不要为了“看起来严谨”补齐所有排列组合、所有内部分支、所有不现实输入和所有字段变化。除非任务明确要求覆盖率目标，否则不要把测试设计成覆盖率驱动

## 测试工具复用

出现重复测试代码时，先判断重复是否表达同一个意图：

| 情况 | 处理方式 |
| --- | --- |
| 相同对象构造、固定配置、常用断言重复出现 | 提取 `newTestXxx`、`mustCreateXxx`、`assertXxx` 等小工具 |
| 多个测试需要同一组基础数据 | 提取 fixture 或 builder，让测试只暴露关键差异 |
| 只有一处使用，或者提取后隐藏了测试意图 | 保持局部代码，不为了抽象而抽象 |
| 发现任务范围外存在大面积重复 | 只说明建议，不顺手做大范围重构 |

工具函数要小而具体，隐藏样板代码，不隐藏测试正在证明什么。避免创建需要大量参数的万能 helper；这种 helper 往往比重复代码更难读

## 命名与注释

测试函数名要短、稳定、易扫读。对同一目标的测试保持统一命名方式，但不要把全部条件塞进函数名

推荐：

- `TestTaskStoreSave`
- `TestTaskStoreFind`
- `TestConfigValidate`
- `TestRunnerCancel`

避免：

- `TestTaskStoreSaveShouldPersistEveryFieldAndReturnTheSameObjectWhenInputIsValid`
- `TestConfigValidateReturnsErrorWhenTimeoutIsZeroAndNameIsEmptyAndModeIsInvalid`

如果测试细节、业务背景或边界选择需要说明，放在测试函数或子用例附近的注释里。注释解释为什么这个行为重要、为什么选这个边界，不要重复函数名已经表达的内容

## 断言策略

断言要证明关键行为，不要机械比对所有字段。尤其是保存、查询、转换大型对象时，优先断言足以证明行为成立的关键字段和可观察效果

常见做法：

- 保存对象后，断言主键、关键业务字段、数量变化或后续可查询，而不是逐字段比对整个对象
- 校验错误时，断言错误存在、错误类型或关键错误信息，而不是依赖完整错误文本
- 状态变化时，断言状态、时间点、计数或事件是否发生，而不是比较无关派生字段
- 只有当完整结构本身就是公共契约时，才使用完整对象比对、快照或 golden file

过度断言会让测试因为无关字段变化频繁失败，也会让测试很长、很难看出真正意图

## 示例

```go
// 保存后应能拿到主键并通过任务名称确认核心持久化链路成立
func TestTaskStoreSave(t *testing.T) {
	task := newTestTask("daily sync")

	saved, err := store.Save(ctx, task)
	require.NoError(t, err)

	require.NotZero(t, saved.ID)
	assert.Equal(t, "daily sync", saved.Name)
}
```

这个测试没有逐字段比对 `saved`，因为主键和关键业务字段已经足以证明保存路径成立。如果字段构造在多个测试重复，`newTestTask` 可以放到共享测试工具里

## 自检清单

提交测试前检查：

- 是否只覆盖正常功能、明确边界和任务要求的错误路径
- 是否避免为了覆盖率或“严谨感”加入大量低价值排列组合
- 重复 setup、fixture、mock 或断言是否已提取为合适的小工具
- 新 helper 是否足够具体，是否没有隐藏测试意图
- 测试函数名是否简洁，是否避免把所有细节写进函数名
- 必要说明是否放在注释里，而不是拉长函数名
- 断言是否集中在关键字段、关键状态和可观察效果上
- 是否避免对大型对象逐字段比较或无意义的完整快照
