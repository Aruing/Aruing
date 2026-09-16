// 收集适配器让结构化角色使用流式运输，完整解析前不改写调用方对象
package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
)

// 持有明确的流式能力，不通过阻塞调用模拟增量
type collectingClient struct {
	// 底层同步流式客户端
	stream Streamer
}

// 为保持完整返回接口的角色绑定流式运输；不支持流式时在装配阶段失败
func NewCollectingClient(inner Client) (Client, error) {
	stream, ok := inner.(Streamer)
	if !ok {
		return nil, errors.New("llm collecting client requires streaming support")
	}
	return &collectingClient{stream: stream}, nil
}

// 收集成功后返回原文，不改写空白或换行
func (c *collectingClient) Generate(ctx context.Context, req Request) (Response, error) {
	content, err := c.collect(ctx, StreamRequest{Request: req})
	if err != nil {
		return Response{}, err
	}
	return Response{Content: content}, nil
}

// 解码进独立临时值，类型错误或断流时保持目标及其嵌套成员不变
func (c *collectingClient) GenerateJSON(ctx context.Context, req Request, out any) error {
	value := reflect.ValueOf(out)
	if !value.IsValid() || value.Kind() != reflect.Pointer || value.IsNil() {
		return errors.New("llm generate json: out must be a non-nil pointer")
	}
	content, err := c.collect(ctx, StreamRequest{Request: req, JSONMode: true})
	if err != nil {
		return err
	}
	temporary := reflect.New(value.Elem().Type())
	if err = json.Unmarshal([]byte(extractJSON(content)), temporary.Interface()); err != nil {
		return fmt.Errorf("%w: %w", ErrJSONParse, err)
	}
	value.Elem().Set(temporary.Elem())
	return nil
}

// 限制收集大小，即使注入的流实现没有自身上限也不无限分配
func (c *collectingClient) collect(ctx context.Context, req StreamRequest) (string, error) {
	var buffer strings.Builder
	_, err := c.stream.Stream(ctx, req, func(delta string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(delta) > maxStreamBytes-buffer.Len() {
			return ErrStreamLimit
		}
		buffer.WriteString(delta)
		return nil
	})
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(buffer.String()) == "" {
		return "", ErrEmptyResponse
	}
	return buffer.String(), nil
}
