package agent

import (
	"context"

	"aruing/internal/core"
)

// 白盒测试共用：假诊断管道，记录收到的运行并返回固定结果
type fakeRunExecutor struct {
	lastRun core.Run
	report  core.Report
	// 若非空则 Execute 返回挂起而非报告
	suspension *core.Suspension
	err        error

	// Resume 相关（实现 SuspendedRunner）
	resumeRunID string
	resumeAns   string
	// Resume 时返回的结果；nil 则用 report
	resumeOutcome *core.Outcome
	// FindSuspended 返回值
	suspendedID string
}

func (f *fakeRunExecutor) Execute(_ context.Context, run core.Run) (core.Outcome, error) {
	f.lastRun = run
	if f.err != nil {
		return core.Outcome{}, f.err
	}
	if f.suspension != nil {
		s := *f.suspension
		if s.RunID == "" {
			s.RunID = run.ID
		}
		if s.SessionID == "" {
			s.SessionID = run.SessionID
		}
		return core.Outcome{Suspension: &s}, nil
	}
	rep := f.report
	rep.RunID = run.ID
	return core.Outcome{Report: &rep}, nil
}

func (f *fakeRunExecutor) Resume(_ context.Context, runID, answer string) (core.Outcome, error) {
	f.resumeRunID = runID
	f.resumeAns = answer
	if f.err != nil {
		return core.Outcome{}, f.err
	}
	if f.resumeOutcome != nil {
		return *f.resumeOutcome, nil
	}
	rep := f.report
	rep.RunID = runID
	return core.Outcome{Report: &rep}, nil
}

func (f *fakeRunExecutor) FindSuspended(sessionID string) string {
	if f.suspendedID != "" {
		return f.suspendedID
	}
	return ""
}
