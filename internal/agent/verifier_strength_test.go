package agent

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/Aruing/Aruing/internal/core"
)

// 标准路径：方向枚举映射数值，强度越界钳位，判定按输入假设顺序返回
func TestJudgeStrength(t *testing.T) {
	body := `{
		"judgements": [
			{"hypothesis_id": "h_1", "direction": "supports", "strength": 1.5},
			{"hypothesis_id": "h_2", "direction": "refutes", "strength": 0.8},
			{"hypothesis_id": "h_3", "direction": "irrelevant", "strength": -0.2}
		]
	}`
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeChatCompletion(w, body)
	})
	verifier, err := NewLLMVerifier(client, newTestFactory(t))
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	hypotheses := []core.Hypothesis{
		{ID: "h_1", RunID: "run_1", Statement: "a"},
		{ID: "h_2", RunID: "run_1", Statement: "b"},
		{ID: "h_3", RunID: "run_1", Statement: "c"},
	}
	evidence := core.Evidence{ID: "ev_1", TaskID: "t_1", ToolName: "k8s", Summary: "CrashLoopBackOff"}

	judgements, err := verifier.JudgeStrength(context.Background(), evidence, hypotheses)
	if err != nil {
		t.Fatalf("judge: %v", err)
	}
	want := []struct {
		direction int
		strength  float64
	}{{1, 1}, {-1, 0.8}, {0, 0}}
	for i, w := range want {
		j := judgements[i]
		if j.HypothesisID != hypotheses[i].ID {
			t.Errorf("judgement[%d] hypothesis = %q, want %q", i, j.HypothesisID, hypotheses[i].ID)
		}
		if j.Direction != w.direction {
			t.Errorf("judgement[%d] direction = %d, want %d", i, j.Direction, w.direction)
		}
		if j.Strength != w.strength {
			t.Errorf("judgement[%d] strength = %v, want %v", i, j.Strength, w.strength)
		}
	}
}

// 漏判假设：严格校验触发业务级重试，上限次后返回模型输出不一致错误
func TestJudgeStrengthMissingHypothesisRetries(t *testing.T) {
	var attempts atomic.Int32
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		writeChatCompletion(w, `{"judgements": [{"hypothesis_id": "h_1", "direction": "supports", "strength": 0.8}]}`)
	})
	verifier, err := NewLLMVerifier(client, newTestFactory(t))
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	hypotheses := []core.Hypothesis{
		{ID: "h_1", RunID: "run_1", Statement: "a"},
		{ID: "h_2", RunID: "run_1", Statement: "b"},
	}
	_, err = verifier.JudgeStrength(context.Background(), core.Evidence{ID: "ev_1"}, hypotheses)
	if !errors.Is(err, ErrLLMOutputInconsistent) {
		t.Fatalf("err = %v, want ErrLLMOutputInconsistent", err)
	}
	if got := attempts.Load(); got != int32(maxVerifyAttempts) {
		t.Errorf("attempts = %d, want %d", got, maxVerifyAttempts)
	}
}

// 错枚举先拒后收：首轮非法方向触发重试，次轮合法输出成功返回
func TestJudgeStrengthRecoversAfterInvalidDirection(t *testing.T) {
	var attempts atomic.Int32
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			writeChatCompletion(w, `{"judgements": [{"hypothesis_id": "h_1", "direction": "maybe", "strength": 0.5}]}`)
			return
		}
		writeChatCompletion(w, `{"judgements": [{"hypothesis_id": "h_1", "direction": "supports", "strength": 0.9}]}`)
	})
	verifier, err := NewLLMVerifier(client, newTestFactory(t))
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	hypotheses := []core.Hypothesis{{ID: "h_1", RunID: "run_1", Statement: "a"}}

	judgements, err := verifier.JudgeStrength(context.Background(), core.Evidence{ID: "ev_1"}, hypotheses)
	if err != nil {
		t.Fatalf("judge: %v", err)
	}
	if judgements[0].Direction != 1 || judgements[0].Strength != 0.9 {
		t.Errorf("judgement = %+v, want supports 0.9", judgements[0])
	}
}

// 提示词契约：strength.md 的输出示例必须能被解析器接受（示例假设编号与文档同步维护）
func TestStrengthPromptContract(t *testing.T) {
	block := fencedJSONBlock(t, strengthPrompt, `"judgements"`)
	judgements, err := parseStrengthOutput([]byte(block), []string{"h_01", "h_02"})
	if err != nil {
		t.Fatalf("prompt example not parseable: %v", err)
	}
	if len(judgements) != 2 || judgements[0].HypothesisID != "h_01" || judgements[0].Direction != 1 {
		t.Errorf("prompt example parsed wrong: %+v", judgements)
	}
}
