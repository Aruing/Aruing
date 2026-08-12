// 集群查询包提供后端级集群工具
//
// 唯一工具通过结构化参数列表直接调用集群命令，不经命令行外壳，不在代码中枚举资源类型或子命令
// 新自定义资源、新子命令依赖环境中的集群命令能力，不要求修改本仓库工具层
//
// 读写策略、命名空间白名单和危险操作确认由注册与调度层的策略控制，本工具只负责表达与执行能力
// 当前单元实现执行器与假集群命令测试；是否接入主编排链路由上层装配决定
package k8s

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"aruing/internal/core"
	"aruing/internal/tools"
	"aruing/internal/tools/summary"
)

const (
	// 注册表与任务中使用的工具名称
	toolName = "k8s"
	// 证据来源标识
	evidenceSource = "kubernetes"
	// 默认单次调用超时
	defaultTimeout = 30 * time.Second
	// 允许的超时硬上限，防止模型或配置给出过大等待
	defaultMaxTimeout = 2 * time.Minute
	// 默认标准输出保留上限
	defaultMaxStdout = 1 << 20
	// 默认标准错误保留上限
	defaultMaxStderr = 256 << 10
)

// 集群命令调用参数的模式
// 只接受参数列表、标准输入与超时秒数，拒绝未知字段以降低提示注入面
var inputSchema = json.RawMessage(`{
  "type": "object",
  "additionalProperties": false,
  "required": ["argv"],
  "properties": {
    "argv": {
      "type": "array",
      "minItems": 1,
      "items": {"type": "string", "minLength": 1}
    },
    "stdin": {"type": "string"},
    "timeoutSeconds": {"type": "integer", "minimum": 1}
  }
}`)

// 构造集群工具所需的运行配置，全部由部署侧注入，不允许模型指定二进制路径
type Config struct {
	// 集群命令可执行文件路径，空值表示使用路径中的默认命令
	KubectlPath string
	// 未指定超时秒数时使用的默认超时，零值表示三十秒
	DefaultTimeout time.Duration
	// 单次调用允许的最大超时，超过则截断到该上限，零值表示两分钟
	MaxTimeout time.Duration
	// 标准输出写入证据前的最大字节数，零值表示一兆字节
	MaxStdoutBytes int
	// 标准错误写入证据前的最大字节数，零值表示二百五十六千字节
	MaxStderrBytes int
}

// 后端级集群工具，通过无命令行外壳的参数列表调用集群命令
type Tool struct {
	// 运行期配置（命令路径、超时与输出上限等）
	config Config
	// 已编译的入参模式，执行时校验参数
	schema *jsonschema.Schema
	// 规格中输入模式的原始副本，供规格方法返回
	specJSON json.RawMessage
}

// 用配置创建集群工具，校验并编译输入模式
func New(cfg Config) (*Tool, error) {
	cfg = applyConfigDefaults(cfg)
	if cfg.KubectlPath == "" {
		return nil, errors.New("kubectl path is required")
	}
	if cfg.DefaultTimeout <= 0 {
		return nil, errors.New("default timeout must be positive")
	}
	if cfg.MaxTimeout < cfg.DefaultTimeout {
		return nil, errors.New("max timeout must be >= default timeout")
	}
	if cfg.MaxStdoutBytes <= 0 || cfg.MaxStderrBytes <= 0 {
		return nil, errors.New("output limits must be positive")
	}

	schema, err := compileSchema(inputSchema)
	if err != nil {
		return nil, err
	}

	return &Tool{
		config:   cfg,
		schema:   schema,
		specJSON: append(json.RawMessage(nil), inputSchema...),
	}, nil
}

// 返回可发现规格，供注册表注册与规划器列举
func (t *Tool) Spec() tools.ToolSpec {
	return tools.ToolSpec{
		Name: toolName,
		Description: "通过集群命令访问 Kubernetes 集群。" +
			"参数 argv 为不含命令自身的参数列表，例如 [\"get\",\"pods\",\"-n\",\"default\"]。" +
			"使用进程直调，不经 shell；默认表格输出会被自动投影为摘要（类型/条数/列/行）。" +
			"大结果集先用 --field-selector / label selector 收窄，或 -o jsonpath={...} 抽取具体字段；需要完整对象时再 -o json。" +
			"若观察已有 evidenceId 且需翻页看表内其它行，用 evidence.read 按 offset/limit 切片，勿重复拉全量。" +
			"可选 stdin 与 timeoutSeconds。",
		InputSchema: append(json.RawMessage(nil), t.specJSON...),
	}
}

// 从本工具写入的 evidence.Raw（resultRaw）中切出表格一页
// 仅支持 exitCode=0 且可解析为 JSON Table 或文本表的 stdout；否则返回错误
func (t *Tool) Slice(raw []byte, q tools.SliceQuery) (tools.SliceView, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return tools.SliceView{}, errors.New("empty raw")
	}
	var rr resultRaw
	if err := json.Unmarshal(raw, &rr); err != nil {
		return tools.SliceView{}, fmt.Errorf("decode k8s raw: %w", err)
	}
	if rr.ExitCode != 0 {
		return tools.SliceView{}, fmt.Errorf("kubectl exitCode=%d, not a table", rr.ExitCode)
	}
	stdout := rr.Stdout
	if strings.TrimSpace(stdout) == "" {
		return tools.SliceView{}, errors.New("empty stdout")
	}
	var proj tableProjection
	var ok bool
	if proj, ok = parseJSONTable(stdout); !ok {
		if proj, ok = parseTextTable(stdout); !ok {
			return tools.SliceView{}, errors.New("stdout is not a tabular projection")
		}
	}
	cols, rows, total, offset, limit := summary.SliceRows(proj.columns, proj.rows, q.Offset, q.Limit)
	return tools.SliceView{
		Total:   total,
		Offset:  offset,
		Limit:   limit,
		Columns: cols,
		Rows:    rows,
	}, nil
}

// 解析并校验参数后执行集群命令，把完整调用结果写入证据
// 模式非法、无法启动进程或上下文取消时返回错误
// 进程启动成功但退出码非零时仍返回证据，错误摘要写入证据错误字段，便于上层纠错
func (t *Tool) Execute(ctx context.Context, args json.RawMessage) (*core.Evidence, error) {
	if t == nil {
		return nil, errors.New("k8s tool is nil")
	}
	if ctx == nil {
		return nil, errors.New("context is required")
	}

	invoke, err := t.parseArgs(args)
	if err != nil {
		return nil, err
	}

	timeout := t.config.DefaultTimeout
	if invoke.TimeoutSeconds > 0 {
		timeout = time.Duration(invoke.TimeoutSeconds) * time.Second
	}
	if timeout > t.config.MaxTimeout {
		timeout = t.config.MaxTimeout
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, t.config.KubectlPath, invoke.Argv...)
	// 明确不使用命令行外壳，标准输入仅在有内容时提供
	if invoke.Stdin != "" {
		cmd.Stdin = strings.NewReader(invoke.Stdin)
	}

	stdout := &limitedBuffer{max: t.config.MaxStdoutBytes}
	stderr := &limitedBuffer{max: t.config.MaxStderrBytes}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	started := time.Now()
	runErr := cmd.Run()
	duration := time.Since(started)

	if runCtx.Err() != nil {
		// 超时或外部取消时优先返回上下文错误，不把半截输出当成功证据
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("kubectl timed out after %s: %w", timeout, runCtx.Err())
		}
		return nil, fmt.Errorf("kubectl cancelled: %w", runCtx.Err())
	}

	exitCode := 0
	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			return nil, fmt.Errorf("run kubectl: %w", runErr)
		}
	}

	stdinBytes := len(invoke.Stdin)
	stdinHash := sha256.Sum256([]byte(invoke.Stdin))
	rawPayload := resultRaw{
		Argv:            append([]string(nil), invoke.Argv...),
		ExitCode:        exitCode,
		Stdout:          stdout.String(),
		Stderr:          stderr.String(),
		StdoutTruncated: stdout.truncated,
		StderrTruncated: stderr.truncated,
		StdinBytes:      stdinBytes,
		StdinSHA256:     hex.EncodeToString(stdinHash[:]),
		DurationMs:      duration.Milliseconds(),
	}
	raw, err := json.Marshal(rawPayload)
	if err != nil {
		return nil, fmt.Errorf("marshal evidence raw: %w", err)
	}

	summary := projectSummary(invoke.Argv, stdout.String(), stderr.String(), exitCode)
	if stdout.truncated || stderr.truncated {
		// 输出超出保留上限被截断：原带不全，追加明确提示，区别于按行数的大表截断
		summary += "\n（原始输出已超过保留上限被截断，请缩小查询范围；残缺原文见 raw）"
	}

	evidence := &core.Evidence{
		Source:      evidenceSource,
		ToolName:    toolName,
		CommandView: commandView(invoke.Argv),
		Summary:     summary,
		Raw:         raw,
	}
	if exitCode != 0 {
		evidence.Error = fmt.Sprintf("kubectl exited with code %d", exitCode)
	}
	return evidence, nil
}

// 调用方传入的参数形态，字段与输入模式对齐
type invokeArgs struct {
	// 集群命令参数列表，不含可执行文件名本身
	Argv []string `json:"argv"`
	// 可选标准输入内容，例如通过管道下发清单
	Stdin string `json:"stdin"`
	// 可选超时秒数，受配置硬上限约束
	TimeoutSeconds int `json:"timeoutSeconds"`
}

// 写入证据原始输出的结构化结果，保留审计与纠错所需字段
type resultRaw struct {
	// 实际传给集群命令的参数列表
	Argv []string `json:"argv"`
	// 进程退出码，零表示成功
	ExitCode int `json:"exitCode"`
	// 截断后的标准输出
	Stdout string `json:"stdout"`
	// 截断后的标准错误
	Stderr string `json:"stderr"`
	// 标准输出是否因超过上限被截断
	StdoutTruncated bool `json:"stdoutTruncated"`
	// 标准错误是否因超过上限被截断
	StderrTruncated bool `json:"stderrTruncated"`
	// 标准输入字节数，不落盘完整内容
	StdinBytes int `json:"stdinBytes"`
	// 标准输入的哈希十六进制摘要
	StdinSHA256 string `json:"stdinSHA256"`
	// 调用耗时，单位毫秒
	DurationMs int64 `json:"durationMs"`
}

// 解析参数并按模式与语言侧约束校验
func (t *Tool) parseArgs(args json.RawMessage) (invokeArgs, error) {
	if len(bytes.TrimSpace(args)) == 0 {
		return invokeArgs{}, errors.New("arguments are required")
	}

	var doc any
	if err := json.Unmarshal(args, &doc); err != nil {
		return invokeArgs{}, fmt.Errorf("arguments are not valid JSON: %w", err)
	}
	if err := t.schema.Validate(doc); err != nil {
		return invokeArgs{}, fmt.Errorf("arguments do not match schema: %w", err)
	}

	decoder := json.NewDecoder(bytes.NewReader(args))
	decoder.DisallowUnknownFields()
	var invoke invokeArgs
	if err := decoder.Decode(&invoke); err != nil {
		return invokeArgs{}, fmt.Errorf("decode arguments: %w", err)
	}
	if len(invoke.Argv) == 0 {
		return invokeArgs{}, errors.New("argv must not be empty")
	}
	for i, arg := range invoke.Argv {
		if arg == "" {
			return invokeArgs{}, fmt.Errorf("argv[%d] must not be empty", i)
		}
	}
	return invoke, nil
}

// 填充配置默认值，空路径回落到默认集群命令名
func applyConfigDefaults(cfg Config) Config {
	if cfg.KubectlPath == "" {
		cfg.KubectlPath = "kubectl"
	}
	if cfg.DefaultTimeout <= 0 {
		cfg.DefaultTimeout = defaultTimeout
	}
	if cfg.MaxTimeout <= 0 {
		cfg.MaxTimeout = defaultMaxTimeout
	}
	if cfg.MaxStdoutBytes <= 0 {
		cfg.MaxStdoutBytes = defaultMaxStdout
	}
	if cfg.MaxStderrBytes <= 0 {
		cfg.MaxStderrBytes = defaultMaxStderr
	}
	return cfg
}

// 编译工具输入模式，供执行校验调用实例
func compileSchema(schema json.RawMessage) (*jsonschema.Schema, error) {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schema))
	if err != nil {
		return nil, fmt.Errorf("input schema: %w", err)
	}
	compiler := jsonschema.NewCompiler()
	const schemaURL = "k8s-input-schema.json"
	if addErr := compiler.AddResource(schemaURL, doc); addErr != nil {
		return nil, fmt.Errorf("input schema: %w", addErr)
	}
	compiled, err := compiler.Compile(schemaURL)
	if err != nil {
		return nil, fmt.Errorf("input schema: %w", err)
	}
	return compiled, nil
}

// 生成给用户看的命令视图，并对常见密钥形态做脱敏
func commandView(argv []string) string {
	parts := make([]string, 0, len(argv)+1)
	parts = append(parts, "kubectl")
	for i := 0; i < len(argv); i++ {
		arg := argv[i]
		if redacted, ok := redactInlineSecret(arg); ok {
			parts = append(parts, redacted)
			continue
		}
		if isSecretFlag(arg) && i+1 < len(argv) {
			parts = append(parts, arg, "***")
			i++
			continue
		}
		parts = append(parts, arg)
	}
	return strings.Join(parts, " ")
}

// 识别内联密钥开关（等号形式）并替换值部分
func redactInlineSecret(arg string) (string, bool) {
	eq := strings.IndexByte(arg, '=')
	if eq <= 0 {
		return "", false
	}
	key := strings.ToLower(arg[:eq])
	if !secretKeyFragment(key) {
		return "", false
	}
	return arg[:eq+1] + "***", true
}

// 识别需要隐藏下一个参数的独立密钥开关
func isSecretFlag(arg string) bool {
	key := strings.ToLower(strings.TrimLeft(arg, "-"))
	return secretKeyFragment(key)
}

// 粗粒度判断参数名是否像密钥，宁可多遮也不把令牌明文写进报告
func secretKeyFragment(key string) bool {
	for _, fragment := range []string{"token", "password", "secret", "authorization", "kubeconfig"} {
		if strings.Contains(key, fragment) {
			return true
		}
	}
	return false
}

// 有上限的字节缓冲，超出后继续吞掉写入以免阻塞子进程管道
type limitedBuffer struct {
	// 允许保留的最大字节数
	max int
	// 已写入缓冲的字节数
	n int
	// 是否已因超限进入丢弃模式
	truncated bool
	// 实际保存的字节内容
	buf bytes.Buffer
}

// 写入数据，超过上限后标记截断并丢弃多余字节
func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.truncated {
		return len(p), nil
	}
	remain := b.max - b.n
	if remain <= 0 {
		b.truncated = true
		return len(p), nil
	}
	if len(p) > remain {
		if _, err := b.buf.Write(p[:remain]); err != nil {
			return 0, err
		}
		b.n += remain
		b.truncated = true
		return len(p), nil
	}
	if _, err := b.buf.Write(p); err != nil {
		return 0, err
	}
	b.n += len(p)
	return len(p), nil
}

// 返回已保留的输出文本
func (b *limitedBuffer) String() string {
	return b.buf.String()
}
