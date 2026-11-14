package openai_test

import (
	"encoding/json"
	"testing"

	"github.com/openai/openai-go/v3"
)

func accumulateChatChunks(t *testing.T, chunks ...string) openai.ChatCompletionAccumulator {
	t.Helper()
	acc := openai.ChatCompletionAccumulator{}
	for i, raw := range chunks {
		var chunk openai.ChatCompletionChunk
		if err := json.Unmarshal([]byte(raw), &chunk); err != nil {
			t.Fatalf("chunk %d: %v", i, err)
		}
		if !acc.AddChunk(chunk) {
			t.Fatalf("AddChunk rejected chunk %d", i)
		}
	}
	return acc
}

const audioChunkPrefix = `{"id":"chatcmpl-audio","object":"chat.completion.chunk","created":1,"model":"gpt-audio",`

var streamedAudioChunks = []string{
	audioChunkPrefix + `"choices":[{"index":0,"delta":{"role":"assistant","audio":{"id":"audio_abc","transcript":"Hel"}}}]}`,
	audioChunkPrefix + `"choices":[{"index":0,"delta":{"audio":{"data":"AAAA","transcript":"lo"}}}]}`,
	audioChunkPrefix + `"choices":[{"index":0,"delta":{"audio":{"data":"BBBB"}}}]}`,
	audioChunkPrefix + `"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
	// The final audio update only carries expires_at and omits finish_reason.
	audioChunkPrefix + `"choices":[{"index":0,"delta":{"audio":{"expires_at":1700000000}}}]}`,
}

func TestChatCompletionAccumulatorStreamedAudio(t *testing.T) {
	acc := accumulateChatChunks(t, streamedAudioChunks...)
	choice := acc.Choices[0]

	if choice.FinishReason != "stop" {
		t.Errorf("FinishReason = %q, want %q", choice.FinishReason, "stop")
	}
	audio := choice.Message.Audio
	if audio.ID != "audio_abc" || audio.Transcript != "Hello" || audio.Data != "AAAABBBB" || audio.ExpiresAt != 1700000000 {
		t.Errorf("Audio = {ID:%q Transcript:%q Data:%q ExpiresAt:%d}, want {audio_abc Hello AAAABBBB 1700000000}",
			audio.ID, audio.Transcript, audio.Data, audio.ExpiresAt)
	}
	if got := choice.Message.ToParam().OfAssistant.Audio.ID; got != "audio_abc" {
		t.Errorf("ToParam audio id = %q, want audio_abc", got)
	}
}

func TestChatCompletionAccumulatorStreamedAudioMatchesNonStreamed(t *testing.T) {
	acc := accumulateChatChunks(t, streamedAudioChunks...)

	var completion openai.ChatCompletion
	if err := json.Unmarshal([]byte(`{"id":"chatcmpl-audio","object":"chat.completion","created":1,"model":"gpt-audio","choices":[{"index":0,"finish_reason":"stop","logprobs":null,"message":{"role":"assistant","content":null,"refusal":null,"audio":{"id":"audio_abc","data":"AAAABBBB","expires_at":1700000000,"transcript":"Hello"}}}]}`), &completion); err != nil {
		t.Fatal(err)
	}
	want, got := completion.Choices[0], acc.Choices[0]
	if got.FinishReason != want.FinishReason ||
		got.Message.Audio.ID != want.Message.Audio.ID ||
		got.Message.Audio.Data != want.Message.Audio.Data ||
		got.Message.Audio.Transcript != want.Message.Audio.Transcript ||
		got.Message.Audio.ExpiresAt != want.Message.Audio.ExpiresAt {
		t.Errorf("streamed choice %+v differs from non-streamed %+v", got.Message.Audio, want.Message.Audio)
	}
}

func TestChatCompletionAccumulatorKeepsCallerAudioEdits(t *testing.T) {
	acc := accumulateChatChunks(t, streamedAudioChunks[:1]...)
	acc.Choices[0].Message.Audio.Transcript = "Replaced "

	var chunk openai.ChatCompletionChunk
	if err := json.Unmarshal([]byte(streamedAudioChunks[1]), &chunk); err != nil {
		t.Fatal(err)
	}
	if !acc.AddChunk(chunk) {
		t.Fatal("AddChunk rejected chunk")
	}
	if got := acc.Choices[0].Message.Audio.Transcript; got != "Replaced lo" {
		t.Errorf("Transcript = %q, want %q", got, "Replaced lo")
	}
}

func TestChatCompletionAccumulatorSumsAllUsageDetails(t *testing.T) {
	const usage = `{"prompt_tokens":100,"completion_tokens":20,"total_tokens":120,"prompt_tokens_details":{"audio_tokens":1,"cache_write_tokens":40,"cached_tokens":10,"image_tokens":30,"text_tokens":69},"completion_tokens_details":{"accepted_prediction_tokens":0,"audio_tokens":2,"reasoning_tokens":5,"rejected_prediction_tokens":0,"text_tokens":13}}`
	acc := accumulateChatChunks(t,
		audioChunkPrefix+`"choices":[{"index":0,"delta":{"role":"assistant","content":"hi"},"finish_reason":"stop"}]}`,
		audioChunkPrefix+`"choices":[],"usage":`+usage+`}`,
	)
	var want openai.CompletionUsage
	if err := json.Unmarshal([]byte(usage), &want); err != nil {
		t.Fatal(err)
	}
	got := acc.Usage
	if got.PromptTokensDetails.CacheWriteTokens != want.PromptTokensDetails.CacheWriteTokens ||
		got.PromptTokensDetails.ImageTokens != want.PromptTokensDetails.ImageTokens ||
		got.PromptTokensDetails.TextTokens != want.PromptTokensDetails.TextTokens ||
		got.CompletionTokensDetails.TextTokens != want.CompletionTokensDetails.TextTokens {
		t.Errorf("usage details = %+v / %+v, want %+v / %+v",
			got.PromptTokensDetails, got.CompletionTokensDetails, want.PromptTokensDetails, want.CompletionTokensDetails)
	}
}
