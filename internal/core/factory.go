// 元数据工厂为所有领域实体提供统一编号和时间来源
//
// 编号采用“开放前缀 + 第七版通用唯一标识符”，时间统一转换为世界协调时
// 默认实现只依赖系统时钟和密码学安全随机源，测试可以注入确定性依赖
// 命令行或接口入口应创建一个实例，并交给实体创建逻辑和编排器共同使用
package core

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// 保存领域实体编号和时间生成所需的可替换依赖
// 默认实例适合在一次运行及其全部子实体之间复用，避免各模块自行生成编号和时间
// 零值可以直接使用，未注入的依赖会回退到安全系统默认值
type Factory struct {
	// 返回当前时间，测试可以注入固定值以保持结果确定
	now func() time.Time
	// 提供编号随机部分，生产环境默认使用密码学安全随机源
	random io.Reader
}

// 创建使用系统时钟和密码学安全随机源的元数据工厂
// 通常在命令行或接口入口组装依赖时调用，再把同一实例传给运行创建和编排流程
func NewFactory() *Factory {
	return &Factory{
		now:    time.Now,
		random: rand.Reader,
	}
}

// 创建使用指定时间和随机源的工厂，仅供包内测试和确定性场景使用
// 固定输入可以稳定验证编号格式和时间字段，不应作为生产入口
func newFactory(now func() time.Time, random io.Reader) *Factory {
	return &Factory{now: now, random: random}
}

// 根据开放前缀生成带时间顺序的全局唯一编号
// 创建运行、问题、节点、目标、任务、证据、判断和报告等实体时调用
// 调用方传入不带分隔符的稳定短前缀，返回“前缀_唯一标识符”格式的编号
// 前缀为空、时间超出格式范围或随机源读取失败时返回错误
func (f *Factory) NewID(prefix string) (string, error) {
	if strings.TrimSpace(prefix) == "" {
		return "", errors.New("ID prefix is required")
	}

	milliseconds := f.Now().UnixMilli()
	if milliseconds < 0 || uint64(milliseconds) > (1<<48)-1 {
		return "", fmt.Errorf("timestamp %d is outside UUIDv7 range", milliseconds)
	}

	source := rand.Reader
	if f != nil && f.random != nil {
		source = f.random
	}
	var entropy [10]byte
	if _, err := io.ReadFull(source, entropy[:]); err != nil {
		return "", fmt.Errorf("read UUIDv7 entropy: %w", err)
	}

	var value [16]byte
	value[0] = byte(milliseconds >> 40)
	value[1] = byte(milliseconds >> 32)
	value[2] = byte(milliseconds >> 24)
	value[3] = byte(milliseconds >> 16)
	value[4] = byte(milliseconds >> 8)
	value[5] = byte(milliseconds)
	value[6] = 0x70 | entropy[0]&0x0f
	value[7] = entropy[1]
	value[8] = 0x80 | entropy[2]&0x3f
	copy(value[9:], entropy[3:])

	raw := hex.EncodeToString(value[:])
	uuid := raw[:8] + "-" + raw[8:12] + "-" + raw[12:16] + "-" + raw[16:20] + "-" + raw[20:]
	return prefix + "_" + uuid, nil
}

// 返回统一转换为世界协调时的当前时间
// 设置实体创建时间、更新时间或证据产生时间时调用，每次调用都读取当前时钟且不缓存结果
func (f *Factory) Now() time.Time {
	if f != nil && f.now != nil {
		return f.now().UTC()
	}
	return time.Now().UTC()
}
