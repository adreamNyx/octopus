package tokenizer

import (
	"sync"

	"github.com/tiktoken-go/tokenizer/codec"
)

// codec.NewO200kBase 每次调用都会重新 regexp2.MustCompile 一遍 o200k 的分词正则，
// 在 2.5GHz Xeon 上实测约 120µs / 37KB 分配；而 Anthropic / OpenAI inbound 解析
// 一个请求会对每个文本字段各调一次 CountTokens（10~30 次），于是单请求就有
// 数毫秒纯耗在重复编译正则上。
//
// Codec.Count/Encode 只读 vocabulary 与 splitRegexp（regexp2 内部用 muRun 互斥，
// 并发安全），Codec.Decode 才会懒初始化 reverseVocabulary —— 本包不使用 Decode，
// 因此进程内共享一个实例是安全的。
var (
	o200kCodecOnce sync.Once
	o200kCodec     *codec.Codec
)

func codecO200k() *codec.Codec {
	o200kCodecOnce.Do(func() {
		o200kCodec = codec.NewO200kBase()
	})
	return o200kCodec
}

// CountTokens 返回 content 在 o200k_base 词表下的 token 数。
// TODO 更多模型（按 model 选择 cl100k_base / o200k_base）
func CountTokens(content, model string) int {
	tc, err := codecO200k().Count(content)
	if err != nil {
		return 0
	}
	return tc
}
