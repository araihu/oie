package domain

import "testing"

func TestReplyBodyPreservesUnmarkedAndInvalidText(t *testing.T) {
	body := "> 🍰\nactual answer"
	got, quote := ReplyBody(body, []TextRange{{Start: 0, End: 4}})
	if got != "actual answer" || quote != "🍰" {
		t.Fatal(got, quote)
	}
	for _, spans := range [][]TextRange{nil, {{-1, 4}}, {{0, 100}}, {{0, 4}, {3, 5}}, {{5, 4}}, {{int(^uint(0) >> 1), int(^uint(0) >> 1)}, {0, 4}}} {
		got, quote = ReplyBody(body, spans)
		if got != body || quote != "" {
			t.Fatal("invalid fallback hid text", spans, got, quote)
		}
	}
	got, quote = ReplyBody("one-X-two-Y-three", []TextRange{{Start: 10, End: 11}, {Start: 4, End: 5}})
	if got != "one--two--three" || quote != "XY" {
		t.Fatal("disjoint Unicode spans", got, quote)
	}
}
