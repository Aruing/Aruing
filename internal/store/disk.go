package store

// 会话单元目录的磁盘存储：每会话一个目录，session.jsonl 首行为会话头，
// 其后逐行追加消息条目（type 自声明的信封格式，未知种类加载时跳过）。
//
// 打开时只扫数据根目录的会话名集合，不读会话正文（惰性加载，冷启动不随
// 历史总量线性变慢）；首次访问会话才读文件建内存索引并持有追加句柄。
// 写路径为内存更新加 O_APPEND 追加一行（无 bufio，写即落）。
//
// 崩溃语义：进程被杀（OS 存活）不丢已完成行；断电残留的末尾半行在加载时
// 跳过警告并在接受追加前截掉（否则半行被新行顶成中间行，整个会话按坏行拒载）；
// 中间坏行启动报错并指明文件与行号（#18 损坏明确失败）。
// 会话最近活跃时间不落盘，由最后条目时间推导（零冗余行）。

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Aruing/Aruing/internal/session"
)

// 会话目录内会话文件的固定名
const sessionFileName = "session.jsonl"

// 当前会话文件格式版本；升级走加载期迁移函数链（旧版本文件自动升格）
const diskFormatVersion = 1

// 信封种类：会话头（仅首行）与消息条目；未知种类加载时跳过（前向兼容）
const (
	diskEntrySession = "session"
	diskEntryMessage = "message"
)

// 会话条目信封：type 自声明本行种类，data 携带对应正文
type diskEntry struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
}

// 会话首行头：v 为格式版本；type 固定为 session，不参与条目种类分发
type diskSessionHeader struct {
	V         int       `json:"v"`
	Type      string    `json:"type"`
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"createdAt"`
}

// 已打开会话的内存形态：会话头、全量消息索引与追加写句柄
type sessionHandle struct {
	header   diskSessionHeader
	messages []session.Message
	file     *os.File
}

// 会话与消息的磁盘存储，实现会话存储接口
// 并发安全（镜像内存实现的单锁语义）；单进程假设，不加文件锁
type DiskStore struct {
	// 保护已知集合与已打开会话映射
	mu sync.Mutex
	// 数据根目录（会话单元目录的父目录）
	root string
	// 启动扫描到的会话目录名集合（存在即会话，正文未读）
	known map[string]struct{}
	// 已加载并持有追加句柄的会话（按会话编号索引）
	open map[string]*sessionHandle
}

// 创建磁盘会话存储：建立数据根目录（0700，含对话与集群证据不世界可读），
// 扫描一层会话目录名建已知集合；不读任何会话正文
func NewDiskStore(ctx context.Context, root string) (*DiskStore, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if root == "" {
		return nil, fmt.Errorf("data dir is required")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create data dir %s: %w", root, err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("scan data dir %s: %w", root, err)
	}
	known := make(map[string]struct{}, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			known[e.Name()] = struct{}{}
		}
	}
	return &DiskStore{
		root:  root,
		known: known,
		open:  make(map[string]*sessionHandle),
	}, nil
}

// 写入新建会话：建会话目录与会话文件，落首行会话头；已存在时返回错误
func (s *DiskStore) CreateSession(ctx context.Context, sess *session.Session) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if sess == nil {
		return fmt.Errorf("session is nil")
	}
	if sess.ID == "" {
		return fmt.Errorf("session id is required")
	}
	if err := checkStorageID(sess.ID); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.known[sess.ID]; ok {
		return fmt.Errorf("session already exists: %s", sess.ID)
	}
	dir := s.sessionDir(sess.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create session dir %s: %w", dir, err)
	}
	path := filepath.Join(dir, sessionFileName)
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("session file already exists: %s", path)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("create session file %s: %w", path, err)
	}
	header := diskSessionHeader{
		V:         diskFormatVersion,
		Type:      diskEntrySession,
		ID:        sess.ID,
		CreatedAt: sess.CreatedAt,
	}
	if err := writeJSONLine(file, header); err != nil {
		file.Close()
		return fmt.Errorf("write session header %s: %w", path, err)
	}
	s.known[sess.ID] = struct{}{}
	s.open[sess.ID] = &sessionHandle{header: header, file: file}
	return nil
}

// 按编号返回会话；最近活跃时间由最后条目推导，不存在时返回会话未找到错误
func (s *DiskStore) GetSession(ctx context.Context, id string) (*session.Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	h, err := s.loadLocked(id)
	if err != nil {
		return nil, err
	}
	return &session.Session{
		ID:        h.header.ID,
		CreatedAt: h.header.CreatedAt,
		UpdatedAt: h.lastActivityLocked(),
	}, nil
}

// 覆盖更新已有会话；磁盘形态最近活跃时间由条目推导（零冗余行），
// 本方法仅校验会话存在，不产生任何写（接口语义与内存实现保持一致）
func (s *DiskStore) UpdateSession(ctx context.Context, sess *session.Session) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if sess == nil {
		return fmt.Errorf("session is nil")
	}
	if sess.ID == "" {
		return fmt.Errorf("session id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := s.loadLocked(sess.ID); err != nil {
		return err
	}
	return nil
}

// 按追加顺序写入一条消息：先追加信封行落盘，再更新内存索引；
// 所属会话不存在时返回会话未找到错误
func (s *DiskStore) AppendMessage(ctx context.Context, message *session.Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if message == nil {
		return fmt.Errorf("message is nil")
	}
	if message.SessionID == "" {
		return fmt.Errorf("message session id is required")
	}
	if message.ID == "" {
		return fmt.Errorf("message id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	h, err := s.loadLocked(message.SessionID)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("marshal message %s: %w", message.ID, err)
	}
	if err := writeJSONLine(h.file, diskEntry{Type: diskEntryMessage, Data: payload}); err != nil {
		return fmt.Errorf("append message to %s: %w", s.sessionPath(message.SessionID), err)
	}
	h.messages = append(h.messages, *message)
	return nil
}

// 按追加顺序返回会话全部消息拷贝；会话不存在时返回会话未找到错误
func (s *DiskStore) ListMessages(ctx context.Context, sessionID string) ([]session.Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	h, err := s.loadLocked(sessionID)
	if err != nil {
		return nil, err
	}
	if len(h.messages) == 0 {
		return nil, nil
	}
	out := make([]session.Message, len(h.messages))
	copy(out, h.messages)
	return out, nil
}

// 列出数据目录下全部会话的只读汇总（最近活跃降序，排序与内存实现同序）。
// 只读路径：不开追加句柄、不进打开缓存、不执行末尾截断与补行修复（修复留给
// 首次加载）。复用整文件解析而非流式扫描：列表是冷路径，单会话峰值内存可
// 接受，且避开逐行扫描器的行长上限（长 checkpoint 行会踩）。
// 坏会话不劫持列表：目录缺会话文件视为非会话静默跳过；解析失败（含中间坏
// 行、会话头不符）跳过并警告一行——打开该会话时仍按加载语义明确失败，
// 这里不伪造状态（#18）。警告走进程级标准错误，与 readSessionFile 末行
// 警告同约定，不经命令层注入的 writer；嵌入式消费若需结构化诊断应整包
// 引入注入，不在此单点分叉
func (s *DiskStore) ListSessions(ctx context.Context) ([]session.SessionSummary, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// 快照已知集合后释放锁再读盘：列表是时点视图，不阻塞其他会话读写
	s.mu.Lock()
	ids := make([]string, 0, len(s.known))
	for id := range s.known {
		ids = append(ids, id)
	}
	s.mu.Unlock()

	summaries := make([]session.SessionSummary, 0, len(ids))
	for _, id := range ids {
		h, _, _, err := readSessionFile(s.sessionPath(id), id)
		if err != nil {
			if errors.Is(err, session.ErrSessionNotFound) {
				// 非会话目录或断电未完成创建：不列入也不警告
				continue
			}
			fmt.Fprintf(os.Stderr, "warn: skip listing session %s: %v\n", id, err)
			continue
		}
		summaries = append(summaries, session.SessionSummary{
			ID:            h.header.ID,
			CreatedAt:     h.header.CreatedAt,
			UpdatedAt:     h.lastActivityLocked(),
			MessageCount:  len(h.messages),
			FirstQuestion: firstUserQuestion(h.messages),
		})
	}
	sortSessionSummaries(summaries)
	return summaries, nil
}

// 会话单元目录路径（目录名即会话编号）
func (s *DiskStore) sessionDir(id string) string {
	return filepath.Join(s.root, id)
}

// 会话文件完整路径
func (s *DiskStore) sessionPath(id string) string {
	return filepath.Join(s.sessionDir(id), sessionFileName)
}

// 加载或复用已打开的会话（调用方持锁）；已知但未加载时读文件建索引。
// 加载时若容忍了末尾半行，先截掉再接受追加：半行一旦被新行顶成中间行，
// 整个会话将按坏行拒载；末行 JSON 完整但无换行时补写换行修复（另一种
// 撕裂形态：数据全落盘只丢末字节换行，内容保留不丢弃）
func (s *DiskStore) loadLocked(id string) (*sessionHandle, error) {
	if id == "" {
		return nil, session.ErrSessionNotFound
	}
	if h, ok := s.open[id]; ok {
		return h, nil
	}
	if _, ok := s.known[id]; !ok {
		return nil, session.ErrSessionNotFound
	}
	h, truncateAt, repairTail, err := readSessionFile(s.sessionPath(id), id)
	if err != nil {
		return nil, err
	}
	file, err := os.OpenFile(s.sessionPath(id), os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open session file for append %s: %w", s.sessionPath(id), err)
	}
	if truncateAt >= 0 {
		if err := file.Truncate(truncateAt); err != nil {
			file.Close()
			return nil, fmt.Errorf("truncate tolerated tail %s: %w", s.sessionPath(id), err)
		}
	}
	// 补行写在截断之后：无换行的末行不补则下次追加把它顶成中间行拒载整个会话
	if repairTail {
		if _, err := file.Write([]byte("\n")); err != nil {
			file.Close()
			return nil, fmt.Errorf("repair tail newline %s: %w", s.sessionPath(id), err)
		}
	}
	h.file = file
	s.open[id] = h
	return h, nil
}

// 最后条目时间推导会话最近活跃时间；无条目时退回会话头创建时间
func (h *sessionHandle) lastActivityLocked() time.Time {
	if n := len(h.messages); n > 0 {
		return h.messages[n-1].CreatedAt
	}
	return h.header.CreatedAt
}

// 会话文件中的一行：裁剪后的正文、该行在文件中的起始字节偏移与
// 行尾是否紧跟换行符（撕裂写不会落盘末尾换行，无换行的末行才是
// 崩溃残迹；带换行的坏行是真损坏，见 readSessionFile）
type sessionLine struct {
	data       []byte
	offset     int64
	terminated bool
}

// 读取并解析会话文件：首行会话头必须完整且归属正确；条目逐行解析，
// 未知种类跳过；末行解析失败且无换行按断电撕裂容忍跳过（返回该行偏移作
// 截断点，调用方在接受追加前截掉，避免半行被顶成中间行）；末行无换行但
// JSON 可解析时内容保留、置补行标记（撕裂写只丢末字节换行的形态），
// 调用方在接受追加前补写换行，同样避免被顶成中间行；带换行的坏行
// 与中间坏行一样报错（真损坏不静默丢，#18）。无容忍半行时截断点返回 -1
func readSessionFile(path, wantID string) (*sessionHandle, int64, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, -1, false, session.ErrSessionNotFound
		}
		return nil, -1, false, fmt.Errorf("read session file %s: %w", path, err)
	}
	// 断电可能只落了目录项与空文件：会话创建未完成，按不存在处理
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, -1, false, session.ErrSessionNotFound
	}
	lines := splitSessionLines(data)
	if len(lines) == 0 {
		return nil, -1, false, fmt.Errorf("session file %s: no header line", path)
	}

	var header diskSessionHeader
	if err := json.Unmarshal(lines[0].data, &header); err != nil {
		return nil, -1, false, fmt.Errorf("session file %s:1: parse header: %w", path, err)
	}
	if header.Type != diskEntrySession {
		return nil, -1, false, fmt.Errorf("session file %s:1: first line is not a session header", path)
	}
	if header.ID != wantID {
		return nil, -1, false, fmt.Errorf("session file %s: header id %q does not match directory %q", path, header.ID, wantID)
	}
	if header.V > diskFormatVersion {
		return nil, -1, false, fmt.Errorf("session file %s: format version %d is newer than supported %d; upgrade aruing", path, header.V, diskFormatVersion)
	}

	truncateAt := int64(-1)
	h := &sessionHandle{header: header}
	for i := 1; i < len(lines); i++ {
		line := lines[i]
		var env diskEntry
		if err := json.Unmarshal(line.data, &env); err != nil {
			// 末行且无换行：断电撕裂写的形态（换行是单次写的末字节，撕裂不会落盘），
			// 容忍跳过（数据丢失但不损坏）；截断点交给调用方，追加前把半行从盘上清掉。
			// 带换行的末行解析失败是真损坏，按中间坏行同样报错（#18）
			if i == len(lines)-1 && !line.terminated {
				fmt.Fprintf(os.Stderr, "warn: %s:%d: truncated tail line skipped: %v\n", path, i+1, err)
				truncateAt = line.offset
				continue
			}
			return nil, -1, false, fmt.Errorf("session file %s:%d: %w", path, i+1, err)
		}
		switch env.Type {
		case diskEntryMessage:
			var msg session.Message
			if err := json.Unmarshal(env.Data, &msg); err != nil {
				return nil, -1, false, fmt.Errorf("session file %s:%d: parse message: %w", path, i+1, err)
			}
			if msg.SessionID != wantID {
				return nil, -1, false, fmt.Errorf("session file %s:%d: message session %q does not match %q", path, i+1, msg.SessionID, wantID)
			}
			h.messages = append(h.messages, msg)
		case diskEntrySession:
			// 会话头只允许出现在首行；重复视为损坏
			return nil, -1, false, fmt.Errorf("session file %s:%d: duplicate session header", path, i+1)
		default:
			// 未知种类跳过：新版本写入的条目不阻断旧进程加载（前向兼容）
		}
	}
	// 末行无换行且未被截断路径接手：写路径单次写带末字节换行，无换行即
	// 撕裂残迹——可解析 ≠ 已终结，内容保留、由调用方补一个换行修复，
	// 否则下次追加把两行顶成一行拒载整个会话
	repairTail := truncateAt < 0 && !lines[len(lines)-1].terminated
	return h, truncateAt, repairTail, nil
}

// 按换行切分行并去首尾空白（含回车）；空行不入结果，行携带起始偏移与
// 行尾是否换行（末段无换行时其偏移不被使用）
func splitSessionLines(data []byte) []sessionLine {
	raw := bytes.Split(data, []byte("\n"))
	lines := make([]sessionLine, 0, len(raw))
	var off int64
	for j, l := range raw {
		chunk := int64(len(l)) + 1 // 含被切掉的换行符
		trimmed := bytes.TrimSpace(l)
		if len(trimmed) > 0 {
			lines = append(lines, sessionLine{data: trimmed, offset: off, terminated: j < len(raw)-1})
		}
		off += chunk
	}
	return lines
}

// 关闭全部已打开会话的追加句柄并清空已打开映射（进程退出前生命周期收口）
// 幂等：重复调用无副作用；关闭后再追加消息会按需重新加载并重开文件。
// 不冲刷任何缓冲（写路径无 bufio，写返回即入页缓存），只为及时归还描述符
func (d *DiskStore) Close() error {
	if d == nil {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	var firstErr error
	for id, h := range d.open {
		if err := h.file.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("close session %s: %w", id, err)
		}
		delete(d.open, id)
	}
	return firstErr
}

// 校验存储编号可安全充当目录名与文件名成分：含路径分隔符或点段（./..）
// 的编号按调用方接线错误拒绝，防止越出数据根。读路径由启动扫描的索引
// 门槛天然挡住越界编号，此处收口写路径，为未来直接受纳外部编号的功能兜底
func checkStorageID(id string) error {
	if id == "." || id == ".." || strings.ContainsAny(id, `/\`) {
		return fmt.Errorf("storage id %q contains path components", id)
	}
	return nil
}

// 行写入的最小写面：*os.File 即满足；拆出接口为测试注入短写故障
type lineWriter interface {
	Write(p []byte) (int, error)
	Seek(offset int64, whence int) (int64, error)
	Truncate(size int64) error
}

// 追加写一行 JSON（值自带换行）；无缓冲直写，写返回即入页缓存。
// 写失败或短写时把文件截断回写前末尾：半行一旦被后续追加顶成中间行，
// 整个会话将按坏行拒载（#18），必须就地清掉；截断也失败时保留残迹，
// 加载侧按末尾半行容忍处理
func writeJSONLine(w lineWriter, v any) error {
	line, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal json line: %w", err)
	}
	// 记录写前末尾：追加模式下 Seek 只取大小，不改写位置
	end, err := w.Seek(0, io.SeekEnd)
	if err != nil {
		return fmt.Errorf("seek end: %w", err)
	}
	buf := append(line, '\n')
	n, err := w.Write(buf)
	if err == nil && n == len(buf) {
		return nil
	}
	if err == nil {
		err = fmt.Errorf("short write: %d of %d bytes", n, len(buf))
	}
	// 回滚到写前末尾，保证半行不入盘；回滚失败与写错误一并报出
	if trErr := w.Truncate(end); trErr != nil {
		return fmt.Errorf("write line: %w; rollback truncate: %w", err, trErr)
	}
	return fmt.Errorf("write line: %w", err)
}
