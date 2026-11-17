package sqlite

import (
	"database/sql/driver"
	"strings"
	"unicode/utf8"

	"github.com/araihu/oie/internal/domain"
	sqlitedriver "modernc.org/sqlite"
)

func init() {
	sqlitedriver.MustRegisterDeterministicScalarFunction("oie_restore_reply_fallback", 2, func(_ *sqlitedriver.FunctionContext, args []driver.Value) (driver.Value, error) {
		original, originalOK := args[0].(string)
		body, bodyOK := args[1].(string)
		if originalOK && bodyOK {
			if spans := restoredReplyFallback(original, body); len(spans) != 0 {
				return jsonText(spans), nil
			}
		}
		return nil, nil
	})
}

// The input is a local wire body and the surviving answer from that exact
// body. Reconstruct only ranges whose removal produces the surviving answer.
func restoredReplyFallback(original, body string) []domain.TextRange {
	if len(body) >= len(original) || !utf8.ValidString(original) || !utf8.ValidString(body) {
		return nil
	}
	if start := strings.LastIndex(original, body); start >= 0 {
		var spans []domain.TextRange
		begin := utf8.RuneCountInString(original[:start])
		end := begin + utf8.RuneCountInString(body)
		if begin > 0 {
			spans = append(spans, domain.TextRange{End: begin})
		}
		if size := utf8.RuneCountInString(original); end < size {
			spans = append(spans, domain.TextRange{Start: end, End: size})
		}
		return spans
	}

	raw, answer := []rune(original), []rune(body)
	remaining := len(answer)
	var spans []domain.TextRange
	for end := len(raw); end > 0; {
		if remaining > 0 && raw[end-1] == answer[remaining-1] {
			remaining--
			end--
			continue
		}
		start := end - 1
		for start > 0 && (remaining == 0 || raw[start-1] != answer[remaining-1]) {
			start--
		}
		if len(spans) == 16 {
			return nil
		}
		spans = append(spans, domain.TextRange{Start: start, End: end})
		end = start
	}
	if remaining != 0 {
		return nil
	}
	return spans
}
