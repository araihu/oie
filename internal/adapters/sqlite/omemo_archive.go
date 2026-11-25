package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func (s *Store) RetainedOMEMO(ctx context.Context, account domain.ID, kind, address, providerID string, own bool) (bool, error) {
	chat, err := s.read.FindConversation(ctx, dbgen.FindConversationParams{AccountID: string(account), Address: address})
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	conversation, err := decodeConversation(chat)
	if err != nil || conversation.Kind != kind {
		return false, err
	}
	row, err := s.read.FindProviderMessage(ctx, dbgen.FindProviderMessageParams{
		AccountID: string(account), ConversationID: chat.ID, ProviderID: providerID, IsOwn: number(own),
	})
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	message, err := decodeMessage(row)
	return message.DirectEncryption == "omemo", err
}

func (s *Store) BindRoomOMEMOEcho(ctx context.Context, echo application.RoomOMEMOEcho) (bool, error) {
	tx, q, err := begin(ctx, s)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = q.AcquireWriteLock(ctx); err != nil {
		return false, err
	}
	if err := checkOMEMOArchiveRetention(ctx, q, echo.AccountID); err != nil {
		return false, err
	}
	row, err := q.GetMessage(ctx, string(echo.MessageID))
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	own, err := decodeMessage(row)
	if err != nil {
		return false, err
	}
	if own.AccountID != echo.AccountID || !own.IsOwn || own.ProviderID != string(own.ID) || own.DirectEncryption != "omemo" ||
		echo.Part < 0 || echo.Part >= len(own.OMEMOWireDigests) || own.OMEMOWireDigests[echo.Part] != echo.EnvelopeID {
		return false, nil
	}
	room, err := q.GetConversation(ctx, string(own.ConversationID))
	if err != nil {
		return false, err
	}
	c, err := decodeConversation(room)
	if err != nil || c.Kind != "room" || c.Address != echo.Room || c.AccountID != echo.AccountID {
		return false, err
	}
	own.ArchiveScope, own.ArchiveID = echo.Room, echo.ArchiveID
	ref := domain.MessageReference{Scope: "room:" + echo.Room, ID: echo.StanzaID, Author: echo.Author}
	changed, err := bindRoomOMEMOReference(ctx, q, c, own, ref)
	if err != nil {
		return false, err
	}
	return changed, tx.Commit()
}

func (s *Store) BindRetainedRoomOMEMOArchive(ctx context.Context, in application.Incoming) (bool, error) {
	tx, q, err := begin(ctx, s)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = q.AcquireWriteLock(ctx); err != nil {
		return false, err
	}
	if err := checkOMEMOArchiveRetention(ctx, q, in.AccountID); err != nil {
		return false, err
	}
	room, err := q.FindConversation(ctx, dbgen.FindConversationParams{AccountID: string(in.AccountID), Address: in.Address})
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	c, err := decodeConversation(room)
	if err != nil || c.Kind != "room" {
		return false, err
	}
	row, err := q.FindProviderMessage(ctx, dbgen.FindProviderMessageParams{
		AccountID: string(in.AccountID), ConversationID: room.ID, ProviderID: in.ProviderID, IsOwn: number(in.IsOwn),
	})
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	saved, err := decodeMessage(row)
	if err != nil || saved.DirectEncryption != "omemo" || saved.Sender.DisplayName != in.SenderName {
		return false, err
	}
	if err := hydrateSenderIdentity(ctx, q, &saved); err != nil {
		return false, err
	}
	if saved.Sender.PersonalAddress != in.SenderAddress {
		return false, nil
	}
	var ref domain.MessageReference
	if len(in.References) == 1 {
		ref = in.References[0]
	}
	saved.ArchiveScope, saved.ArchiveID = in.ArchiveScope, in.ArchiveID
	changed, err := bindRoomOMEMOReference(ctx, q, c, saved, ref)
	if err != nil {
		return false, err
	}
	return changed, tx.Commit()
}

func checkOMEMOArchiveRetention(ctx context.Context, q *dbgen.Queries, account domain.ID) error {
	expected, guarded := application.ArchiveRetentionRevisionFrom(ctx)
	if !guarded {
		return nil
	}
	revision, err := q.GetArchiveRetentionRevision(ctx, string(account))
	if err != nil {
		return err
	}
	if revision != expected {
		return application.ErrStateConflict
	}
	return nil
}

func bindRoomOMEMOReference(ctx context.Context, q *dbgen.Queries, c domain.Conversation, m domain.Message, ref domain.MessageReference) (bool, error) {
	if ref.ID != "" {
		owners, err := q.FindVerifiedRoomOccurrence(ctx, dbgen.FindVerifiedRoomOccurrenceParams{
			AccountID: string(c.AccountID), RoomAddress: c.Address, RemoteID: ref.ID,
		})
		if err != nil {
			return false, err
		}
		if len(owners) != 0 && (len(owners) != 1 || owners[0] != string(m.ID)) {
			return false, nil
		}
	}
	if m.ArchiveID != "" {
		alias, err := q.GetArchiveAlias(ctx, dbgen.GetArchiveAliasParams{
			AccountID: string(c.AccountID), Scope: c.Address, ArchiveID: m.ArchiveID,
		})
		switch {
		case errors.Is(err, sql.ErrNoRows):
			if err := archiveAlias(ctx, q, m, string(m.ID)); err != nil {
				return false, err
			}
		case err != nil || alias != string(m.ID):
			return false, err
		}
	}
	if ref.ID == "" {
		return false, nil
	}
	n, err := q.InsertMessageReference(ctx, dbgen.InsertMessageReferenceParams{
		MessageID: string(m.ID), Scope: ref.Scope, RemoteID: ref.ID, Author: ref.Author, SourceHash: m.SourceBodyHash(),
	})
	if err != nil || n == 0 {
		return false, err
	}
	if err := q.InvalidateSharedContent(ctx, string(m.ID)); err != nil {
		return false, err
	}
	if err := hydrateMessageWithdrawal(ctx, q, &m); err != nil {
		return false, err
	}
	now := time.Now().UTC()
	if m.IsWithdrawn() {
		// An archive can deliver the authenticated notice before its target.
		// Recompute the summary after attaching the reference.
		if err := refreshConversationSummary(ctx, q, m.ConversationID, now); err != nil {
			return false, err
		}
	}
	_, err = emitCanonicalMessageEvent(ctx, q, "message.updated", m, now)
	return err == nil, err
}
