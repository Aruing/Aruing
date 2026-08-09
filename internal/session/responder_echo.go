package session

import (
	"context"
)

// 固定模板回复，不跑诊断；长期可留作测试假实现
type EchoResponder struct{}

// 返回「收到：」加用户原文，模式为基线，不产生诊断运行
func (EchoResponder) Respond(_ context.Context, in RespondInput) (RespondOutput, error) {
	return RespondOutput{
		Content: "收到：" + in.UserText,
		Mode:    ModeBaseline,
	}, nil
}
