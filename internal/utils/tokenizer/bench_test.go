package tokenizer

import (
	"strings"
	"testing"
)

// 真实的请求体片段：一段 system prompt + 若干条对话消息 + 工具定义。
var benchParts = []string{
	"You are a helpful assistant that answers in Chinese and cites sources when possible.",
	"请解释一下 Go 的 GOMAXPROCS 与 goroutine 调度器之间的关系，并给出一个示例。",
	`{"type":"function","function":{"name":"get_weather","description":"Get the current weather for a location","parameters":{"type":"object","properties":{"location":{"type":"string"},"unit":{"type":"string"}}}}}`,
	strings.Repeat("The quick brown fox jumps over the lazy dog. ", 40),
	strings.Repeat("中文长文本片段，用于测试分词器在真实多字节内容上的性能。", 40),
}

func BenchmarkCountTokensShort(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = CountTokens(benchParts[0], "gpt-4o")
	}
}

func BenchmarkCountTokensLong(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = CountTokens(benchParts[3], "gpt-4o")
	}
}

// 模拟 Anthropic inbound 的逐字段计数路径：一个请求里多次调用 CountTokens。
func BenchmarkCountTokensMultiPart(b *testing.B) {
	for i := 0; i < b.N; i++ {
		total := 0
		for round := 0; round < 4; round++ {
			for _, p := range benchParts {
				total += CountTokens(p, "gpt-4o")
			}
		}
		_ = total
	}
}

func BenchmarkCountTokensParallel(b *testing.B) {
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = CountTokens(benchParts[1], "gpt-4o")
		}
	})
}
