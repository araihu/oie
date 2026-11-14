package sqlite

import (
	"database/sql/driver"
	"encoding/json"

	"github.com/araihu/oie/internal/domain"

	sqlitedriver "modernc.org/sqlite"
)

func init() {
	sqlitedriver.MustRegisterDeterministicScalarFunction("oie_search_match", 2, func(_ *sqlitedriver.FunctionContext, args []driver.Value) (driver.Value, error) {
		text, _ := args[0].(string)
		query, _ := args[1].(string)
		if matchSharedMetadata(text, query) {
			return int64(1), nil
		}
		return int64(0), nil
	})
	sqlitedriver.MustRegisterDeterministicScalarFunction("oie_reply_body", 2, func(_ *sqlitedriver.FunctionContext, args []driver.Value) (driver.Value, error) {
		text, _ := args[0].(string)
		payload, ok := args[1].(string)
		if !ok {
			return text, nil
		}
		var ref domain.ReplyReference
		if err := json.Unmarshal([]byte(payload), &ref); err != nil {
			return nil, err
		}
		body, _ := domain.ReplyBody(text, ref.Fallback)
		return body, nil
	})

}
