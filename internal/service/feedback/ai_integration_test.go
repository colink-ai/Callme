package feedback

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"callme/internal/model"
	"callme/internal/service/agent"
)

func TestOpenAICompatibleManualDraftAndLearning(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("authorization header = %q", got)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request: %v", err)
		}
		var req struct {
			Stream bool `json:"stream"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		if req.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"{\\\"title\\\":\\\"流式知识\\\",\\\"question\\\":\\\"如何使用流式整理？\\\",\\\"content\\\":\\\"## 步骤\\\",\\\"confidence\\\":0.8,\\\"reason\\\":\\\"测试\\\"}\"}}]}\n\n"))
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
			return
		}
		if strings.Contains(string(body), "candidates") {
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"candidates\":[{\"title\":\"测试知识\",\"question\":\"如何整理？\",\"content\":\"## 步骤\\n\\n按输入整理。\",\"confidence\":1.7,\"reason\":\"测试返回\"}]}"}}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"title\":\"测试知识\",\"question\":\"如何整理？\",\"content\":\"## 步骤\\n\\n按输入整理。\",\"confidence\":1.7,\"reason\":\"测试返回\"}"}}]}`))
	}))
	defer server.Close()

	spec := agent.AgentSpec{APIURL: server.URL + "/", APIToken: "test-token", DefaultModel: "test-model"}
	ctx := context.Background()
	draft, err := runAIManualDraft(ctx, spec, ManualDraftRequest{
		PublishTargets: []model.KnowledgePublishTarget{model.KnowledgePublishLocal},
		Description:    "把客服答案整理成知识。",
	})
	if err != nil {
		t.Fatalf("manual draft: %v", err)
	}
	if draft.Title != "测试知识" || draft.Confidence != 1.7 {
		t.Fatalf("unexpected manual draft: %+v", draft)
	}

	var streamed strings.Builder
	streamDraft, raw, err := runAIManualDraftStream(ctx, spec, ManualDraftRequest{Description: "流式整理"}, func(delta string) error {
		streamed.WriteString(delta)
		return nil
	})
	if err != nil {
		t.Fatalf("manual draft stream: %v", err)
	}
	if streamDraft.Title != "流式知识" || raw != streamed.String() {
		t.Fatalf("unexpected stream draft=%+v raw=%q streamed=%q", streamDraft, raw, streamed.String())
	}

	candidates, err := runAILearning(ctx, spec, "user: 如何整理？\nassistant: 按输入整理。")
	if err != nil {
		t.Fatalf("ai learning: %v", err)
	}
	if len(candidates) != 1 || candidates[0].Confidence != 1 {
		t.Fatalf("confidence should be clamped to 1, got %+v", candidates)
	}
	for _, path := range paths {
		if path != "/chat/completions" {
			t.Fatalf("unexpected API path %q", path)
		}
	}
}

func TestOpenAICompatibleRuntimeLearningEdit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Stream bool `json:"stream"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"# 修订\"}}]}\n\n"))
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"\\n内容\"}}]}\n\n"))
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{\"choices\":[{\"message\":{\"content\":\"```markdown\\n# 修订\\n内容\\n```\"}}]}"))
	}))
	defer server.Close()

	spec := agent.AgentSpec{APIURL: server.URL, APIToken: "test-token", DefaultModel: "test-model"}
	asset := &model.RuntimeLearningAsset{AgentType: "hermes", AssetType: model.RuntimeLearningAssetSkill}
	out, err := runAIRuntimeLearningEdit(context.Background(), spec, asset, "# 原文", "压缩表述")
	if err != nil {
		t.Fatalf("runtime edit: %v", err)
	}
	if out != "# 修订\n内容" {
		t.Fatalf("unexpected edit output %q", out)
	}
	var deltas []string
	streamOut, err := runAIRuntimeLearningEditStream(context.Background(), spec, asset, "# 原文", "压缩表述", func(delta string) error {
		deltas = append(deltas, delta)
		return nil
	})
	if err != nil {
		t.Fatalf("runtime edit stream: %v", err)
	}
	if streamOut != "# 修订\n内容" || len(deltas) != 2 {
		t.Fatalf("unexpected stream output=%q deltas=%+v", streamOut, deltas)
	}
}

func TestRealAIManualDraft(t *testing.T) {
	if os.Getenv("CALLME_REAL_AI_TEST") != "1" {
		t.Skip("set CALLME_REAL_AI_TEST=1 with CALLME_REAL_AI_URL, CALLME_REAL_AI_TOKEN, and CALLME_REAL_AI_MODEL to run")
	}
	url := strings.TrimSpace(os.Getenv("CALLME_REAL_AI_URL"))
	token := strings.TrimSpace(os.Getenv("CALLME_REAL_AI_TOKEN"))
	modelName := strings.TrimSpace(os.Getenv("CALLME_REAL_AI_MODEL"))
	if url == "" || token == "" || modelName == "" {
		t.Fatal("CALLME_REAL_AI_URL, CALLME_REAL_AI_TOKEN, and CALLME_REAL_AI_MODEL are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	draft, err := runAIManualDraft(ctx, agent.AgentSpec{
		APIURL:       url,
		APIToken:     token,
		DefaultModel: modelName,
	}, ManualDraftRequest{
		PublishTargets: []model.KnowledgePublishTarget{model.KnowledgePublishLocal},
		Description:    "当用户询问 Callme 如何验证 AI 能力时，建议先运行健康检查，再发送一句简短业务问题，确认返回内容可读且没有错误。",
	})
	if err != nil {
		t.Fatalf("real AI manual draft failed: %v", err)
	}
	if strings.TrimSpace(draft.Title) == "" || strings.TrimSpace(draft.Content) == "" {
		t.Fatalf("real AI draft missing required content: %+v", draft)
	}
}
