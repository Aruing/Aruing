package tools

import (
	"encoding/json"
	"sync"
)

// 轮内观察索引：按证据编号保存基线工具原始输出，供 evidence.read 按需切片
// 进程内、线程安全；轮末由调用方 Discard 释放
// 不落盘、不跨轮；权威内容仍以观察环内 Raw 为准
type ObservationIndex struct {
	mu   sync.Mutex
	byID map[string]ObsRecord
}

// 索引中的一条观察快照
type ObsRecord struct {
	// 工具原始输出全量拷贝
	Raw json.RawMessage
	// 产出该观察的工具名（用于查 Registry 取 Slicer）
	ToolName string
}

// 创建空索引
func NewObservationIndex() *ObservationIndex {
	return &ObservationIndex{byID: make(map[string]ObsRecord)}
}

// 写入或覆盖一条观察；id 或 raw 为空时忽略
func (idx *ObservationIndex) Put(id string, rec ObsRecord) {
	if idx == nil || id == "" || len(rec.Raw) == 0 {
		return
	}
	idx.mu.Lock()
	defer idx.mu.Unlock()
	if idx.byID == nil {
		idx.byID = make(map[string]ObsRecord)
	}
	raw := append(json.RawMessage(nil), rec.Raw...)
	idx.byID[id] = ObsRecord{Raw: raw, ToolName: rec.ToolName}
}

// 按编号读取；不存在返回 ok=false
func (idx *ObservationIndex) Get(id string) (ObsRecord, bool) {
	if idx == nil || id == "" {
		return ObsRecord{}, false
	}
	idx.mu.Lock()
	defer idx.mu.Unlock()
	rec, ok := idx.byID[id]
	if !ok {
		return ObsRecord{}, false
	}
	out := ObsRecord{
		ToolName: rec.ToolName,
		Raw:      append(json.RawMessage(nil), rec.Raw...),
	}
	return out, true
}

// 删除给定编号（轮末释放）；空切片无操作
func (idx *ObservationIndex) Discard(ids []string) {
	if idx == nil || len(ids) == 0 {
		return
	}
	idx.mu.Lock()
	defer idx.mu.Unlock()
	for _, id := range ids {
		delete(idx.byID, id)
	}
}
