package tools

import (
	"encoding/json"
	"testing"
)

func TestObservationIndexPutGetDiscard(t *testing.T) {
	idx := NewObservationIndex()
	raw := json.RawMessage(`{"stdout":"x"}`)
	idx.Put("e_1", ObsRecord{Raw: raw, ToolName: "k8s"})

	got, ok := idx.Get("e_1")
	if !ok {
		t.Fatal("expected hit")
	}
	if got.ToolName != "k8s" || string(got.Raw) != string(raw) {
		t.Fatalf("got %#v", got)
	}
	// Get 返回拷贝：改返回值不影响索引
	got.Raw[0] = 'X'
	again, _ := idx.Get("e_1")
	if again.Raw[0] != '{' {
		t.Fatal("index raw must be independent of Get copy")
	}

	if _, ok := idx.Get("missing"); ok {
		t.Fatal("missing id should miss")
	}

	idx.Discard([]string{"e_1"})
	if _, ok := idx.Get("e_1"); ok {
		t.Fatal("after Discard must miss")
	}
}

// 空 id / 空 raw 忽略；nil 接收方安全
func TestObservationIndexIgnoresEmpty(t *testing.T) {
	var nilIdx *ObservationIndex
	nilIdx.Put("e", ObsRecord{Raw: json.RawMessage(`{}`)})
	if _, ok := nilIdx.Get("e"); ok {
		t.Fatal("nil index Get")
	}
	nilIdx.Discard([]string{"e"})

	idx := NewObservationIndex()
	idx.Put("", ObsRecord{Raw: json.RawMessage(`{}`)})
	idx.Put("e", ObsRecord{})
	if _, ok := idx.Get("e"); ok {
		t.Fatal("empty raw must not store")
	}
}
