// 决策状态深拷贝（挂起快照与循环内状态隔离用）
package agent

import "slices"

// 深拷贝取证决策状态：动作池、执行史与待答复动作都不与原状态共享底层数组
func cloneAcquireState(state AcquireState) AcquireState {
	cloned := AcquireState{
		Belief:   state.Belief,
		Executed: slices.Clone(state.Executed),
	}
	cloned.Actions = make([]ActionProposal, len(state.Actions))
	copy(cloned.Actions, state.Actions)
	for i := range cloned.Actions {
		cloned.Actions[i].Argv = slices.Clone(state.Actions[i].Argv)
		cloned.Actions[i].Outcomes = slices.Clone(state.Actions[i].Outcomes)
		cloned.Actions[i].Matrix = make([][]float64, len(state.Actions[i].Matrix))
		for j, row := range state.Actions[i].Matrix {
			cloned.Actions[i].Matrix[j] = slices.Clone(row)
		}
	}
	if state.Asked != nil {
		asked := *state.Asked
		asked.Argv = slices.Clone(state.Asked.Argv)
		asked.Outcomes = slices.Clone(state.Asked.Outcomes)
		asked.Matrix = make([][]float64, len(state.Asked.Matrix))
		for j, row := range state.Asked.Matrix {
			asked.Matrix[j] = slices.Clone(row)
		}
		cloned.Asked = &asked
	}
	return cloned
}
