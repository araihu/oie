package application

import "testing"

func TestReactionEmojiValidationPreservesSequences(t *testing.T) {
	for _, value := range []string{"👍", "👍🏽", "❤️", "👨‍👩‍👧‍👦", "🇧🇷", "1️⃣", "©️"} {
		got, err := NormalizeReactionEmojis([]string{value})
		if err != nil || len(got) != 1 || got[0] != value {
			t.Fatal(value, got, err)
		}
	}
	for _, values := range [][]string{{""}, {"plain"}, {"∀"}, {"→"}, {"🜁"}, {"🏽"}, {"🇧"}, {"👍👍"}, {"a⃣რ"}, {"👍", "👍"}, {"\x00"}, {"1"}, {"a"}, {"\ufe0f"}} {
		if _, err := NormalizeReactionEmojis(values); err == nil {
			t.Fatal("invalid set accepted", values)
		}
	}
	if _, err := NormalizeReactionEmojis([]string{"👍", "❤️", "😂", "😢", "🔥", "🎉", "👀", "✅", "❌"}); err == nil {
		t.Fatal("unbounded own set")
	}
	empty, err := NormalizeReactionEmojis(nil)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatal("clear lost empty array semantics", empty, err)
	}
}
