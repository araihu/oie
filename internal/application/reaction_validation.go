package application

import (
	"github.com/araihu/oie/internal/domain"
	"github.com/rivo/uniseg"
	"slices"
	"unicode/utf8"
)

// NormalizeReactionEmojis preserves variation selectors, modifiers and ZWJ
// sequences while rejecting text, multiple graphemes and duplicate selections.
func NormalizeReactionEmojis(values []string) ([]string, error) {
	if len(values) > 8 {
		return nil, ErrInvalid
	}
	out := append([]string{}, values...)
	for _, value := range out {
		if !utf8.ValidString(value) || utf8.RuneCountInString(value) > 32 || value == "" {
			return nil, ErrInvalid
		}
		g := uniseg.NewGraphemes(value)
		if !g.Next() || g.Str() != value || g.Next() {
			return nil, ErrInvalid
		}
		base := false
		for _, r := range value {
			if reactionEmojiBase(r) {
				base = true
			}
		}
		chars := []rune(value)
		if chars[len(chars)-1] == 0x20e3 && (chars[0] >= '0' && chars[0] <= '9' || chars[0] == '#' || chars[0] == '*') {
			base = true
		}
		if len(chars) == 2 && chars[0] >= 0x1f1e6 && chars[0] <= 0x1f1ff && chars[1] >= 0x1f1e6 && chars[1] <= 0x1f1ff {
			base = true
		}
		if !base {
			return nil, ErrInvalid
		}
	}
	slices.Sort(out)
	for i := 1; i < len(out); i++ {
		if out[i] == out[i-1] {
			return nil, ErrInvalid
		}
	}
	return out, nil
}
func reactionPlanAllows(plan domain.OutgoingReaction, values []string) bool {
	max := plan.MaxEmojis
	if max < 0 {
		return false
	}
	if max > 8 {
		max = 8
	}
	if len(values) > max {
		return false
	}
	if plan.AllowedEmojis != nil {
		for _, v := range values {
			if !slices.Contains(plan.AllowedEmojis, v) {
				return false
			}
		}
	}
	return true
}
