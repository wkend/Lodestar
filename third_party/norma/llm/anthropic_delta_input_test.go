package llm

import "testing"

// Zhipu's Anthropic-compatible endpoint reports input 0 in message_start and the
// real input in message_delta. The parser must keep that input rather than drop it.
func TestParseAnthropicFrameKeepsDeltaInputWhenStartIsZero(t *testing.T) {
	start, ok, err := parseAnthropicFrame(`{"type":"message_start","message":{"usage":{"input_tokens":0,"output_tokens":1}}}`)
	if !ok || err != nil {
		t.Fatalf("start: ok=%v err=%v", ok, err)
	}
	delta, ok, err := parseAnthropicFrame(`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":13,"output_tokens":7}}`)
	if !ok || err != nil {
		t.Fatalf("delta: ok=%v err=%v", ok, err)
	}
	var acc Accumulator
	acc.Add(start)
	acc.Add(delta)
	if acc.Usage.InputTokens != 13 {
		t.Fatalf("input = %d, want 13", acc.Usage.InputTokens)
	}
	if acc.Usage.OutputTokens != 7 {
		t.Fatalf("output = %d, want 7", acc.Usage.OutputTokens)
	}
}

// Anthropic re-echoes the same input in message_delta. It must not be counted twice.
func TestParseAnthropicFrameDoesNotDoubleCountEchoedInput(t *testing.T) {
	start, _, _ := parseAnthropicFrame(`{"type":"message_start","message":{"usage":{"input_tokens":20,"output_tokens":1}}}`)
	delta, _, _ := parseAnthropicFrame(`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":20,"output_tokens":5}}`)
	var acc Accumulator
	acc.Add(start)
	acc.Add(delta)
	if acc.Usage.InputTokens != 20 {
		t.Fatalf("input = %d, want 20 (no double count)", acc.Usage.InputTokens)
	}
}
