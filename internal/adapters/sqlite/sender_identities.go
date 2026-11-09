package sqlite

import (
	"context"
	"encoding/json"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/domain"
)

func validPersonalAddress(address string) bool {
	return address != "" && len(address) <= 1024 && utf8.ValidString(address) && strings.TrimSpace(address) == address && strings.IndexFunc(address, unicode.IsControl) < 0
}

// Identity enrichment never changes the authored body or content/send revision.
// The adapter supplied the address; the stored message supplies all owner IDs.
func recordSenderIdentity(ctx context.Context, q *dbgen.Queries, old, in domain.Message) (int64, error) {
	address := in.Sender.PersonalAddress
	if !validPersonalAddress(address) {
		return 0, nil
	}
	return q.InsertMessageSenderIdentity(ctx, dbgen.InsertMessageSenderIdentityParams{MessageID: string(old.ID), AccountID: string(old.AccountID), ConversationID: string(old.ConversationID), PersonalAddress: address, SourceHash: bodyHash(in.OriginalText)})
}

func hydrateSenderIdentity(ctx context.Context, q *dbgen.Queries, m *domain.Message) error {
	m.Sender.PersonalAddress = ""
	rows, err := q.ListCanonicalSenderIdentities(ctx, dbgen.ListCanonicalSenderIdentitiesParams{CanonicalID: string(m.ID), AccountID: string(m.AccountID), ConversationID: string(m.ConversationID)})
	if err != nil {
		return err
	}
	if len(rows) >= 129 {
		return nil
	}
	address := ""
	for _, row := range rows {
		var source domain.Message
		if err = json.Unmarshal([]byte(row.MessagePayload), &source); err != nil {
			return err
		}
		if bodyHash(source.OriginalText) != row.SourceHash || source.AccountID != m.AccountID || source.ConversationID != m.ConversationID || !validPersonalAddress(row.PersonalAddress) {
			return nil
		}
		if address != "" && address != row.PersonalAddress {
			return nil
		}
		address = row.PersonalAddress
	}
	m.Sender.PersonalAddress = address
	return nil
}
