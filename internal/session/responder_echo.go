package session

import (
	"context"
)

// 固定模板回复，不跑诊断；长期可留作测试假实现
type EchoResponder struct{}

// 返回「收到：」加用户原文，Mode 为 baseline，不产生 Run
func (EchoResponder) Respond(_ context.Context, in RespondInput) (RespondOutput, error) {
	return RespondOutput{
		Content: "收到：" + in.UserText,
		Mode:    ModeBaseline,
	}, nil
}
