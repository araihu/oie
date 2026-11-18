package sqlite

import (
	"context"
	"database/sql/driver"
	"strings"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/domain"
	sqlitedriver "modernc.org/sqlite"
)

func init() {
	sqlitedriver.MustRegisterDeterministicScalarFunction("oie_room_correction_provider_id", 2, func(_ *sqlitedriver.FunctionContext, args []driver.Value) (driver.Value, error) {
		author, _ := args[0].(string)
		remote, _ := args[1].(string)
		return roomCorrectionProviderID(author, remote), nil
	})
}

func roomCorrectionProviderID(author, remote string) string {
	return "room:" + bodyHash(author+"\x00"+remote)
}

func correctionProviderMatches(provider, scope, author, remote string) bool {
	return provider == remote || (strings.HasPrefix(scope, "xmpp:correct:room:") && provider == roomCorrectionProviderID(author, remote))
}

// The room archive provider key includes the occupant. Recover the command
// ID only from the exact correction fact of the current content revision.
func observedRoomEditFact(ctx context.Context, q *dbgen.Queries, source domain.Message) (*dbgen.MessageCorrectionFact, error) {
	if !strings.HasPrefix(source.ProviderID, "room:") {
		return nil, nil
	}
	facts, err := q.GetCorrectionFacts(ctx, string(source.ID))
	if err != nil || len(facts) != 1 {
		return nil, err
	}
	fact := facts[0]
	if fact.TargetID == "" || fact.SourceHash != source.SourceBodyHash() ||
		!correctionProviderMatches(source.ProviderID, fact.Scope, fact.Author, fact.RemoteID) {
		return nil, nil
	}
	return &fact, nil
}
