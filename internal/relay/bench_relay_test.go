package relay

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	dbpkg "github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/relay/balancer"
	"github.com/bestruirui/octopus/internal/transformer/inbound"
	"github.com/bestruirui/octopus/internal/transformer/outbound"
	"github.com/gin-gonic/gin"
)

func benchSetupRelayDB(b *testing.B) context.Context {
	b.Helper()

	if dbpkg.GetDB() != nil {
		_ = dbpkg.Close()
	}
	balancer.Reset()

	dbPath := filepath.Join(b.TempDir(), "octopus-relay-bench.db")
	if err := dbpkg.InitDB("sqlite", dbPath, false); err != nil {
		b.Fatalf("InitDB failed: %v", err)
	}
	if err := op.InitCache(); err != nil {
		b.Fatalf("InitCache failed: %v", err)
	}
	b.Cleanup(func() {
		balancer.Reset()
		_ = dbpkg.Close()
	})
	return context.Background()
}

// 典型的长上下文请求：system + 多轮对话 + 工具定义。
var benchConversation = func() string {
	var sb strings.Builder
	sb.WriteString(`"messages":[`)
	sb.WriteString(`{"role":"system","content":"You are a senior Go engineer. Answer in Chinese, cite files with absolute paths, and always propose tests."},`)
	for i := 0; i < 8; i++ {
		sb.WriteString(`{"role":"user","content":"请分析下面这段 Go 代码的性能问题并给出改进建议：func handle(c *gin.Context) { body, _ := io.ReadAll(c.Request.Body); var req map[string]any; json.Unmarshal(body, &req); c.JSON(200, req) } 这段代码在 200 QPS 下的内存分配情况如何？"},`)
		sb.WriteString(`{"role":"assistant","content":"这段代码每次请求都会 io.ReadAll 分配一份完整的 body 拷贝，并且用 map[string]any 反序列化会带来大量小对象分配。建议使用结构体 + json.Decoder，并复用 sync.Pool 缓冲。"},`)
	}
	sb.WriteString(`{"role":"user","content":"好的，请给出完整实现。"}]`)
	return sb.String()
}()

var benchTools = `"tools":[{"type":"function","function":{"name":"read_file","description":"Read a text file from disk","parameters":{"type":"object","properties":{"path":{"type":"string"},"offset":{"type":"integer"}},"required":["path"]}}}]`

func benchOpenAIChatBody(modelName string) string {
	return `{"model":"` + modelName + `",` + benchConversation + `,` + benchTools + `}`
}

func benchAnthropicBody(modelName string) string {
	var sb strings.Builder
	sb.WriteString(`{"model":"` + modelName + `","max_tokens":1024,"system":"You are a senior Go engineer. Answer in Chinese and cite files.",`)
	sb.WriteString(`"messages":[`)
	for i := 0; i < 8; i++ {
		if i%2 == 0 {
			sb.WriteString(`{"role":"user","content":[{"type":"text","text":"请分析下面这段 Go 代码的性能问题并给出改进建议：每次请求都 io.ReadAll 整个 body，然后用 map[string]any 反序列化，在 200 QPS 下内存分配情况如何？"}]},`)
		} else {
			sb.WriteString(`{"role":"assistant","content":[{"type":"text","text":"建议使用结构体 + json.Decoder，并复用 sync.Pool 缓冲，避免每请求一份 body 拷贝。"}]},`)
		}
	}
	sb.WriteString(`{"role":"user","content":[{"type":"text","text":"好的，请给出完整实现。"}]}],`)
	sb.WriteString(`"tools":[{"name":"read_file","description":"Read a text file from disk","input_schema":{"type":"object","properties":{"path":{"type":"string"}}}}]}`)
	return sb.String()
}

// BenchmarkHandlerOpenAIChatNonStream 测 OpenAI chat 入站 → OpenAI chat 渠道的完整转发路径。
func BenchmarkHandlerOpenAIChatNonStream(b *testing.B) {
	gin.SetMode(gin.TestMode)
	ctx := benchSetupRelayDB(b)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-1","object":"chat.completion","created":1,"model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1200,"completion_tokens":8,"total_tokens":1208}}`))
	}))
	defer upstream.Close()

	channel := &model.Channel{
		Name:     "bench-openai-chat",
		Type:     outbound.OutboundTypeOpenAIChat,
		Enabled:  true,
		BaseUrls: []model.BaseUrl{{URL: upstream.URL + "/v1"}},
		Model:    "gpt-4o",
		Keys:     []model.ChannelKey{{Enabled: true, ChannelKey: "bench-key"}},
	}
	if err := op.ChannelCreate(channel, ctx); err != nil {
		b.Fatalf("ChannelCreate failed: %v", err)
	}
	group := &model.Group{Name: "bench-openai-group", Mode: model.GroupModeFailover}
	if err := op.GroupCreate(group, ctx); err != nil {
		b.Fatalf("GroupCreate failed: %v", err)
	}
	if err := op.GroupItemAdd(&model.GroupItem{GroupID: group.ID, ChannelID: channel.ID, ModelName: "gpt-4o", Priority: 1, Weight: 1}, ctx); err != nil {
		b.Fatalf("GroupItemAdd failed: %v", err)
	}

	body := benchOpenAIChatBody("bench-openai-group")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Set("api_key_id", 1)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		Handler(inbound.InboundTypeOpenAIChat, c)
		if recorder.Code != http.StatusOK {
			b.Fatalf("unexpected status %d: %s", recorder.Code, recorder.Body.String())
		}
	}
}

// BenchmarkHandlerAnthropicToOpenAI 测 Anthropic 入站 → OpenAI 渠道（跨协议转换 + 逐字段分词）。
func BenchmarkHandlerAnthropicToOpenAI(b *testing.B) {
	gin.SetMode(gin.TestMode)
	ctx := benchSetupRelayDB(b)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-2","object":"chat.completion","created":1,"model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1200,"completion_tokens":8,"total_tokens":1208}}`))
	}))
	defer upstream.Close()

	channel := &model.Channel{
		Name:     "bench-anthropic-upstream",
		Type:     outbound.OutboundTypeOpenAIChat,
		Enabled:  true,
		BaseUrls: []model.BaseUrl{{URL: upstream.URL + "/v1"}},
		Model:    "gpt-4o",
		Keys:     []model.ChannelKey{{Enabled: true, ChannelKey: "bench-key"}},
	}
	if err := op.ChannelCreate(channel, ctx); err != nil {
		b.Fatalf("ChannelCreate failed: %v", err)
	}
	group := &model.Group{Name: "bench-anthropic-group", Mode: model.GroupModeFailover}
	if err := op.GroupCreate(group, ctx); err != nil {
		b.Fatalf("GroupCreate failed: %v", err)
	}
	if err := op.GroupItemAdd(&model.GroupItem{GroupID: group.ID, ChannelID: channel.ID, ModelName: "gpt-4o", Priority: 1, Weight: 1}, ctx); err != nil {
		b.Fatalf("GroupItemAdd failed: %v", err)
	}

	body := benchAnthropicBody("bench-anthropic-group")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Set("api_key_id", 1)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		Handler(inbound.InboundTypeAnthropic, c)
		if recorder.Code != http.StatusOK {
			b.Fatalf("unexpected status %d: %s", recorder.Code, recorder.Body.String())
		}
	}
}
