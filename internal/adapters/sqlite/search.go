package sqlite

import (
	"database/sql/driver"

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
}
