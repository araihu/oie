package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
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
	if len(in.References) == 0 && in.ReplyReference == nil && in.Sender.PersonalAddress == "" {
		return nil
	}
	row, err := q.GetMessage(ctx, id)
	if err != nil {
		return err
	}
	if row.AccountID != string(in.AccountID) || (in.ConversationID != "" && row.ConversationID != string(in.ConversationID)) {
		return application.ErrInvalid
	}
	old, err := decodeMessage(row)
	if err != nil {
		return err
	}
	changed, err := recordSenderIdentity(ctx, q, old, in)
	if err != nil {
		return err
	}
	incomingHash, retainedHash := in.SourceBodyHash(), old.SourceBodyHash()
	attachmentRoom := ""
	if len(in.References) > 0 && incomingHash != retainedHash {
		attachmentRoom, err = verifiedAttachmentReferenceRoom(ctx, q, old, in)
		if err != nil {
			return err
		}
	}
	for i, r := range in.References {
		if i >= 8 {
			break
		}
		if !referenceValid(r) || r.Scope == "" {
			continue
		}
		verifiedAttachment := attachmentRoom != "" && r.Scope == "room:"+attachmentRoom && strings.HasPrefix(r.Author, attachmentRoom+"/")
		referenceHash := incomingHash
		if verifiedAttachment {
			referenceHash = retainedHash
			// Earlier ingestions could attach the verified identifier using the
			// hash of the full uploaded body, which cannot identify the caption.
			n, err := q.DeleteMessageReference(ctx, dbgen.DeleteMessageReferenceParams{
				MessageID: id, Scope: r.Scope, RemoteID: r.ID, Author: r.Author, SourceHash: incomingHash,
			})
			if err != nil {
				return err
			}
			changed += n
		}
		n, err := q.InsertMessageReference(ctx, dbgen.InsertMessageReferenceParams{MessageID: id, Scope: r.Scope, RemoteID: r.ID, Author: r.Author, SourceHash: referenceHash})
		if err != nil {
			return err
		}
		changed += n
	}
	if in.ReplyReference != nil && retainedHash == incomingHash && referenceValid(in.ReplyReference.Target) && len(in.ReplyReference.Fallback) <= 16 {
		n, err := q.InsertMessageReply(ctx, dbgen.InsertMessageReplyParams{MessageID: id, BodyHash: incomingHash, Payload: jsonText(in.ReplyReference)})
		if err != nil {
			return err
		}
		if n > 0 && emit && len(in.ReplyReference.Fallback) > 0 {
			if err = refreshMentionAfterReply(ctx, q, &old); err != nil {
				return err
			}
		}
		changed += n
	}
	if changed > 0 {
		if err = q.InvalidateSharedContent(ctx, id); err != nil {
			return err
		}
	}
	if changed > 0 && emit {
		_, err = emitCanonicalMessageEvent(ctx, q, "message.updated", old, time.Now().UTC())
	}
	return err
}

func verifiedAttachmentReferenceRoom(ctx context.Context, q *dbgen.Queries, old, in domain.Message) (string, error) {
	if !in.IsOwn || !old.IsOwn || in.OwnOriginID != old.ID || old.ProviderID != string(old.ID) {
		return "", nil
	}
	matched := old.MatchesAttachmentWireBody(in.OriginalText) &&
		old.MatchesWireMentions(domain.WireMentionProof{Evidence: in.WireMentions, Digest: in.WireMentionDigest})
	if !matched {
		var err error
		matched, err = q.HasArchivedAttachmentWireBody(ctx, dbgen.HasArchivedAttachmentWireBodyParams{
			MessageID: string(old.ID), AccountID: string(old.AccountID), ConversationID: string(old.ConversationID), WireBody: in.OriginalText,
			ObservedMentions: string(in.WireMentions), ObservedMentionDigest: in.WireMentionDigest,
		})
		if err != nil || !matched {
			return "", err
		}
	}
	row, err := q.GetConversation(ctx, string(old.ConversationID))
	if err != nil {
		return "", err
	}
	chat, err := decodeConversation(row)
	if err != nil || chat.Kind != "room" {
		return "", err
	}
	return chat.Address, nil
}

func stripInboundQuote(m *domain.Message, ref domain.ReplyReference) bool {
	if m.WireBodySHA256 != "" || m.OutboundReply != nil {
		return false
	}
	body, _ := domain.ReplyBody(m.OriginalText, ref.Fallback)
	if body == m.OriginalText {
		return false
	}
	m.WireBodySHA256 = m.SourceBodyHash()
	m.OriginalText = body
	return true
}

func scrubInboundFallback(ref *domain.ReplyReference) {
	if len(ref.Fallback) > 0 {
		ref.WireSHA256 = ref.WireDigest()
	}
	ref.Fallback = []domain.TextRange{}
}

func readMessageWireReply(ctx context.Context, q *dbgen.Queries, id, hash string) (*domain.ReplyReference, error) {
	payload, err := q.GetMessageReply(ctx, dbgen.GetMessageReplyParams{MessageID: id, BodyHash: hash})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	return decodeReplyReference(payload)
}

func decodeReplyReference(payload string) (*domain.ReplyReference, error) {
	if payload == "" {
		return nil, nil
	}
	var reply domain.ReplyReference
	if err := json.Unmarshal([]byte(payload), &reply); err != nil {
		return nil, err
	}
	return &reply, nil
}

func expiredReplyReference(ctx context.Context, q *dbgen.Queries, conversation domain.ID, ref domain.MessageReference) (bool, error) {
	expired, err := q.IsExpiredReplyReference(ctx, dbgen.IsExpiredReplyReferenceParams{
		ConversationID: string(conversation), RemoteID: ref.ID, Scope: ref.Scope, Author: ref.Author,
	})
	if err != nil || expired {
		return expired, err
	}
	return q.IsDiscardedReplyReference(ctx, dbgen.IsDiscardedReplyReferenceParams{
		OwnerID: string(conversation), RemoteID: ref.ID, Scope: ref.Scope, Author: ref.Author,
	})
}

func unavailableExpiredQuote(ctx context.Context, q *dbgen.Queries, account, conversation domain.ID, ref domain.MessageReference) (bool, error) {
	expired, err := expiredReplyReference(ctx, q, conversation, ref)
	if err != nil || !expired {
		return false, err
	}
	retained, err := q.FindReplyTargets(ctx, dbgen.FindReplyTargetsParams{
		AccountID: string(account), ConversationID: string(conversation), RemoteID: ref.ID, Scope: ref.Scope, Author: ref.Author,
	})
	return len(retained) == 0, err
}

func hydrateReply(ctx context.Context, q *dbgen.Queries, m *domain.Message) error {
	if m.IsWithdrawn() {
		return nil
	}
	if m.OutboundReply != nil {
		domain.ProjectOutboundReply(m)
		targetID, err := resolveMessageAlias(ctx, q, m.OutboundReply.TargetID)
		if err != nil {
			return err
		}
		target, err := q.GetMessage(ctx, string(targetID))
		if errors.Is(err, sql.ErrNoRows) {
			expired, err := q.IsExpiredMessage(ctx, string(targetID))
			if expired {
				m.Reply.Excerpt, m.Reply.SenderName = "", ""
				m.Reply.OriginalExpired = true
			}
			return err
		}
		if err != nil {
			return err
		}
		m.Reply.MessageID = targetID
		m.Reply.Available = target.AccountID == string(m.AccountID) && target.ConversationID == string(m.ConversationID)
		if m.Reply.Available {
			withdrawn, err := decodeMessage(target)
			if err != nil {
				return err
			}
			if err = hydrateMessageWithdrawal(ctx, q, &withdrawn); err != nil {
				return err
			}
			if withdrawn.IsWithdrawn() {
				m.Reply.Excerpt = ""
				m.Reply.OriginalExpired = true
			}
		}
		return nil
	}
	sourceID := m.ID
	if m.ProjectionSourceID != "" {
		sourceID = m.ProjectionSourceID
	}
	payload, err := q.GetMessageReply(ctx, dbgen.GetMessageReplyParams{MessageID: string(sourceID), BodyHash: m.SourceBodyHash()})
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
	body, fallback := m.OriginalText, ""
	if m.WireBodySHA256 == "" {
		body, fallback = domain.ReplyBody(m.OriginalText, ref.Fallback)
	}
	m.Reply = &domain.MessageReply{BodyText: body, Excerpt: boundedText(fallback, 512)}
	rows, err := q.FindReplyTargets(ctx, dbgen.FindReplyTargetsParams{AccountID: string(m.AccountID), ConversationID: string(m.ConversationID), RemoteID: ref.Target.ID, Scope: ref.Target.Scope, Author: ref.Target.Author})
	if err != nil {
		return err
	}
	if len(rows) >= 129 {
		return nil
	}
	candidates := map[domain.ID]domain.Message{}
	for _, row := range rows {
		var raw domain.Message
		if err = json.Unmarshal([]byte(row.Payload), &raw); err != nil {
			return err
		}
		if raw.SourceBodyHash() != row.SourceHash {
			return nil
		}
		id, err := resolveMessageAlias(ctx, q, raw.ID)
		if err != nil {
			return err
		}
		if id == m.ID {
			continue
		}
		root, err := q.GetMessage(ctx, string(id))
		if err != nil {
			return err
		}
		target, err := decodeMessage(root)
		if err != nil {
			return err
		}
		if err = hydrateMessageProjection(ctx, q, &target); err != nil {
			return err
		}
		candidates[id] = target
	}
	if len(candidates) != 1 {
		expired, err := expiredReplyReference(ctx, q, m.ConversationID, ref.Target)
		if expired {
			m.Reply.Excerpt = ""
			m.Reply.OriginalExpired = true
		}
		return err
	}
	var target domain.Message
	for _, candidate := range candidates {
		target = candidate
	}
	if target.IsWithdrawn() {
		m.Reply.Available = true
		m.Reply.MessageID = target.ID
		m.Reply.SenderName = boundedText(target.Sender.DisplayName, 300)
		m.Reply.Excerpt = ""
		m.Reply.OriginalExpired = true
		return nil
	}
	excerpt := target.OriginalText
	metadataID := target.ID
	if target.ProjectionSourceID != "" {
		metadataID = target.ProjectionSourceID
	}
	if target.OutboundReply != nil {
		excerpt = target.OutboundReply.BodyText
	} else if target.WireBodySHA256 == "" {
		payload, err = q.GetMessageReply(ctx, dbgen.GetMessageReplyParams{MessageID: string(metadataID), BodyHash: target.SourceBodyHash()})
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil {
			var nested domain.ReplyReference
			if err = json.Unmarshal([]byte(payload), &nested); err != nil {
				return err
			}
			excerpt, _ = domain.ReplyBody(excerpt, nested.Fallback)
		}
	}
	m.Reply.Available = true
	m.Reply.MessageID = target.ID
	m.Reply.SenderName = boundedText(target.Sender.DisplayName, 300)
	m.Reply.Excerpt = boundedText(excerpt, 512)
	return nil
}

func (s *Store) hydrateReplies(ctx context.Context, items []domain.Message) ([]domain.Message, error) {
	return hydrateMessageRows(ctx, s.read, items)
}

func hydrateMessageRows(ctx context.Context, q *dbgen.Queries, items []domain.Message) ([]domain.Message, error) {
	for i := range items {
		if err := hydrateMessageProjection(ctx, q, &items[i]); err != nil {
			return nil, err
		}
		if err := hydrateReply(ctx, q, &items[i]); err != nil {
			return nil, err
		}
	}
	return items, nil
}

func (s *Store) MessageContext(ctx context.Context, conversation, target domain.ID, limit int) (domain.MessageContext, error) {
	tx, q, err := beginRead(ctx, s)
	if err != nil {
		return domain.MessageContext{}, err
	}
	defer func() { _ = tx.Rollback() }()
	canonical, err := resolveMessageAlias(ctx, q, target)
	if err != nil {
		return domain.MessageContext{}, err
	}
	window := domain.MessageContext{TargetID: canonical}
	if canonical != target {
		window.RequestedTargetID = target
	}
	target = canonical
	row, err := q.GetTimelineMessage(ctx, string(target))
	if err != nil {
		return window, notFound(err)
	}
	if row.ConversationID != string(conversation) {
		return window, application.ErrNotFound
	}
	targetMS := row.CreatedAtMs
	if row.TimelineAtMs != nil {
		targetMS = *row.TimelineAtMs
	}
	olderLimit := (limit-1)/2 + 1
	newerLimit := limit - olderLimit
	before, err := q.MessagesAtOrBeforeTarget(ctx, dbgen.MessagesAtOrBeforeTargetParams{ConversationID: string(conversation), TargetID: string(target), TargetMs: &targetMS, PageLimit: int64(olderLimit + 1)})
	if err != nil {
		return window, err
	}
	after, err := q.MessagesAfterTarget(ctx, dbgen.MessagesAfterTargetParams{ConversationID: string(conversation), TargetID: string(target), TargetMs: &targetMS, PageLimit: int64(newerLimit + 1)})
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
	window.Items, err = hydrateMessageRows(ctx, q, window.Items)
	if err != nil {
		return window, err
	}
	return window, tx.Commit()
}

func hydrateReferences(ctx context.Context, q *dbgen.Queries, m *domain.Message) error {
	id, err := resolveMessageAlias(ctx, q, m.ID)
	if err != nil {
		return err
	}
	rows, err := q.ListCanonicalMessageReferences(ctx, string(id))
	if err != nil {
		return err
	}
	return applyMessageReferences(m, rows)
}

func applyMessageReferences(m *domain.Message, rows []dbgen.ListCanonicalMessageReferencesRow) error {
	m.References = nil
	seen := map[domain.MessageReference]bool{}
	for _, row := range rows {
		var source domain.Message
		if err := json.Unmarshal([]byte(row.MessagePayload), &source); err != nil {
			return err
		}
		if source.SourceBodyHash() != row.SourceHash {
			continue
		}
		ref := domain.MessageReference{Scope: row.Scope, ID: row.RemoteID, Author: row.Author}
		if !seen[ref] {
			m.References = append(m.References, ref)
			seen[ref] = true
		}
		if len(m.References) == 8 {
			break
		}
	}
	return nil
}
