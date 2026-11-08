package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"time"
	"unicode/utf8"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func bodyHash(s string) string { sum := sha256.Sum256([]byte(s)); return hex.EncodeToString(sum[:]) }
func referenceValid(r domain.MessageReference) bool {
	return r.ID != "" && len(r.ID) <= 4096 && len(r.Scope) <= 4096 && len(r.Author) <= 4096 && utf8.ValidString(r.ID+r.Scope+r.Author)
}
func boundedText(s string, max int) string {
	chars := []rune(s)
	if len(chars) > max {
		return string(chars[:max])
	}
	return s
}

// Metadata is an append-only index of observed protocol facts. Content hashes
// bind Unicode spans to their exact body, and preserve collision evidence rather
// than silently selecting a wrong original. No content or send revision changes.
func recordReplyMetadata(ctx context.Context, q *dbgen.Queries, id string, in domain.Message, emit bool) error {
	if len(in.References) == 0 && in.ReplyReference == nil {
		return nil
	}
	row, err := q.GetMessage(ctx, id)
	if err != nil {
		return err
	}
	if row.AccountID != string(in.AccountID) {
		return application.ErrInvalid
	}
	old, err := decodeMessage(row)
	if err != nil {
		return err
	}
	changed := int64(0)
	for i, r := range in.References {
		if i >= 8 {
			break
		}
		if !referenceValid(r) || r.Scope == "" {
			continue
		}
		n, err := q.InsertMessageReference(ctx, dbgen.InsertMessageReferenceParams{MessageID: id, Scope: r.Scope, RemoteID: r.ID, Author: r.Author, SourceHash: bodyHash(in.OriginalText)})
		if err != nil {
			return err
		}
		changed += n
	}
	if in.ReplyReference != nil && old.OriginalText == in.OriginalText && referenceValid(in.ReplyReference.Target) && len(in.ReplyReference.Fallback) <= 16 {
		n, err := q.InsertMessageReply(ctx, dbgen.InsertMessageReplyParams{MessageID: id, BodyHash: bodyHash(in.OriginalText), Payload: jsonText(in.ReplyReference)})
		if err != nil {
			return err
		}
		changed += n
	}
	if changed > 0 {
		if err = q.InvalidateSharedContent(ctx, id); err != nil {
			return err
		}
	}
	if changed > 0 && emit {
		_, err = change(ctx, q, "message.updated", old, time.Now().UTC())
	}
	return err
}

func hydrateReply(ctx context.Context, q *dbgen.Queries, m *domain.Message) error {
	if m.OutboundReply != nil {
		domain.ProjectOutboundReply(m)
		target, err := q.GetMessage(ctx, string(m.OutboundReply.TargetID))
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		m.Reply.Available = target.AccountID == string(m.AccountID) && target.ConversationID == string(m.ConversationID)
		return nil
	}

	payload, err := q.GetMessageReply(ctx, dbgen.GetMessageReplyParams{MessageID: string(m.ID), BodyHash: bodyHash(m.OriginalText)})
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var ref domain.ReplyReference
	if err = json.Unmarshal([]byte(payload), &ref); err != nil {
		return err
	}
	body, fallback := domain.ReplyBody(m.OriginalText, ref.Fallback)
	m.Reply = &domain.MessageReply{BodyText: body, Excerpt: boundedText(fallback, 512)}
	rows, err := q.FindReplyTargets(ctx, dbgen.FindReplyTargetsParams{AccountID: string(m.AccountID), ConversationID: string(m.ConversationID), RemoteID: ref.Target.ID, Scope: ref.Target.Scope, Author: ref.Target.Author})
	if err != nil {
		return err
	}
	if len(rows) != 1 || rows[0].ID == string(m.ID) {
		return nil
	}
	var target domain.Message
	if err = json.Unmarshal([]byte(rows[0].Payload), &target); err != nil {
		return err
	}
	if bodyHash(target.OriginalText) != rows[0].SourceHash {
		return nil
	}
	excerpt := target.OriginalText
	if payload, err = q.GetMessageReply(ctx, dbgen.GetMessageReplyParams{MessageID: string(target.ID), BodyHash: bodyHash(excerpt)}); err == nil {
		var nested domain.ReplyReference
		if err = json.Unmarshal([]byte(payload), &nested); err != nil {
			return err
		}
		excerpt, _ = domain.ReplyBody(excerpt, nested.Fallback)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	m.Reply.Available = true
	m.Reply.MessageID = target.ID
	m.Reply.SenderName = boundedText(target.Sender.DisplayName, 300)
	m.Reply.Excerpt = boundedText(excerpt, 512)
	return nil
}
func (s *Store) hydrateReplies(ctx context.Context, items []domain.Message) ([]domain.Message, error) {
	for i := range items {
		if err := hydrateReply(ctx, s.queries, &items[i]); err != nil {
			return nil, err
		}
	}
	return items, nil
}

func (s *Store) MessageContext(ctx context.Context, conversation, target domain.ID, limit int) (domain.MessageContext, error) {
	window := domain.MessageContext{TargetID: target}
	row, err := s.queries.GetMessage(ctx, string(target))
	if err != nil {
		return window, notFound(err)
	}
	if row.ConversationID != string(conversation) {
		return window, application.ErrNotFound
	}
	olderLimit := (limit-1)/2 + 1
	newerLimit := limit - olderLimit
	before, err := s.queries.MessagesAtOrBeforeTarget(ctx, dbgen.MessagesAtOrBeforeTargetParams{ConversationID: string(conversation), TargetID: string(target), PageLimit: int64(olderLimit + 1)})
	if err != nil {
		return window, err
	}
	after, err := s.queries.MessagesAfterTarget(ctx, dbgen.MessagesAfterTargetParams{ConversationID: string(conversation), TargetID: string(target), PageLimit: int64(newerLimit + 1)})
	if err != nil {
		return window, err
	}
	window.HasOlder = len(before) > olderLimit
	window.HasNewer = len(after) > newerLimit
	if window.HasOlder {
		before = before[:olderLimit]
	}
	if window.HasNewer {
		after = after[:newerLimit]
	}
	slices.Reverse(after)
	all := append(after, before...)
	window.Items, err = decodeMessages(all)
	if err != nil {
		return window, err
	}
	window.Items, err = s.hydrateReplies(ctx, window.Items)
	return window, err
}

func hydrateReferences(ctx context.Context, q *dbgen.Queries, m *domain.Message) error {
	rows, err := q.GetMessageReferences(ctx, dbgen.GetMessageReferencesParams{MessageID: string(m.ID), SourceHash: bodyHash(m.OriginalText)})
	if err != nil {
		return err
	}
	m.References = nil
	for _, row := range rows {
		m.References = append(m.References, domain.MessageReference{Scope: row.Scope, ID: row.RemoteID, Author: row.Author})
	}
	return nil
}
