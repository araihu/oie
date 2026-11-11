package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	"reflect"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

func decodeReaction(row dbgen.MessageReactionCommand) (domain.PendingMessageReaction, error) {
	var p domain.PendingMessageReaction
	err := json.Unmarshal([]byte(row.Payload), &p)
	if err == nil && (string(p.Command.ID) != row.ID || string(p.Command.RevisionID) != row.RevisionID || string(p.Command.MessageID) != row.MessageID || string(p.Command.AccountID) != row.AccountID || string(p.Command.ConversationID) != row.ConversationID || p.Command.State != row.State) {
		err = application.ErrProtocolUnavailable
	}
	return p, err
}
func reactionTarget(ctx context.Context, q *dbgen.Queries, account, conversation domain.ID, ref domain.MessageReference) (domain.ID, error) {
	rows, err := q.FindReplyTargets(ctx, dbgen.FindReplyTargetsParams{AccountID: string(account), ConversationID: string(conversation), RemoteID: ref.ID, Scope: ref.Scope, Author: ref.Author})
	if err != nil {
		return "", err
	}
	if len(rows) > 128 {
		return "", nil
	}
	var target domain.ID
	for _, row := range rows {
		m, err := decodeMessage(dbgen.Message{Payload: row.Payload})
		if err != nil {
			return "", err
		}
		if row.SourceHash != bodyHash(m.OriginalText) {
			continue
		}
		id, err := resolveMessageAlias(ctx, q, domain.ID(row.ID))
		if err != nil {
			return "", err
		}
		if target != "" && target != id {
			return "", nil
		}
		target = id
	}
	return target, nil
}
func reactionEvent(ctx context.Context, q *dbgen.Queries, id domain.ID, at time.Time) error {
	row, err := q.GetMessage(ctx, string(id))
	if err != nil {
		return notFound(err)
	}
	m, err := decodeMessage(row)
	if err != nil {
		return err
	}
	_, err = emitCanonicalMessageEvent(ctx, q, "message.updated", m, at)
	return err
}
func readReactions(ctx context.Context, q *dbgen.Queries, id domain.ID) (application.MessageReactionCandidate, error) {
	var out application.MessageReactionCandidate
	canonical, err := resolveMessageAlias(ctx, q, id)
	if err != nil {
		return out, err
	}
	row, err := q.GetMessage(ctx, string(canonical))
	if err != nil {
		return out, notFound(err)
	}
	m, err := decodeMessage(row)
	if err != nil {
		return out, err
	}
	if err = hydrateMessageProjection(ctx, q, &m); err != nil {
		return out, err
	}
	if err = hydrateReferences(ctx, q, &m); err != nil {
		return out, err
	}
	out.Source = m
	state := domain.MessageReactions{MessageID: m.ID, AccountID: m.AccountID, ConversationID: m.ConversationID, OwnEmojis: []string{}, Items: []domain.ReactionItem{}, Actors: []domain.ReactionActor{}, MaxEmojis: 8}
	crow, err := q.GetConversation(ctx, string(m.ConversationID))
	if err != nil {
		return out, err
	}
	c, err := decodeConversation(crow)
	if err != nil {
		return out, err
	}
	var privacy dbgen.GetRoomReactionPrivacyRow
	if c.Kind == "room" {
		privacy, err = getRoomReactionPrivacy(ctx, q, m.AccountID, c.Address)
		if err != nil {
			return out, err
		}
	}
	publicRoom := privacy.Public == 1
	refs, err := q.ListCanonicalMessageReferences(ctx, string(m.ID))
	if err != nil {
		return out, err
	}
	type reference struct {
		ID     string `json:"id"`
		Scope  string `json:"scope"`
		Author string `json:"author"`
		Unique bool   `json:"unique"`
	}
	safeRefs := []reference{}
	unique := map[string]bool{}
	for _, ref := range refs {
		found, known := unique[ref.RemoteID]
		if !known {
			target, err := reactionTarget(ctx, q, m.AccountID, m.ConversationID, domain.MessageReference{ID: ref.RemoteID})
			if err != nil {
				return out, err
			}
			found = target == m.ID
			unique[ref.RemoteID] = found
		}
		safeRefs = append(safeRefs, reference{ref.RemoteID, ref.Scope, ref.Author, found})
	}
	observations, err := q.ReactionObservationsForTargets(ctx, dbgen.ReactionObservationsForTargetsParams{AccountID: string(m.AccountID), Address: c.Address, Kind: c.Kind, MessageID: string(m.ID), References: jsonText(safeRefs), RoomEpoch: privacy.Epoch})
	if err != nil {
		return out, err
	}
	heads := map[string]reactionHead{}
	resolved := map[domain.MessageReference]domain.ID{}
	for _, observation := range observations {
		var in application.IncomingReaction
		if err = json.Unmarshal([]byte(observation.Payload), &in); err != nil {
			return out, err
		}
		if in.Kind != c.Kind {
			continue
		}
		if in.Kind == "room" && !in.IsOwn {
			opaque := domain.IsRoomReactionActor(in.Actor)
			if privacy.IdentityMode == application.RoomReactionsAnonymous ||
				opaque && privacy.IdentityMode == application.RoomReactionsNonanonymous || !opaque && !publicRoom {
				continue
			}
		}
		var linked domain.ID
		if observation.CommandID != "" {
			cmd, err := q.GetMessageReactionCommand(ctx, observation.CommandID)
			if err != nil {
				return out, err
			}
			if cmd.AccountID == string(m.AccountID) && cmd.ConversationID == string(m.ConversationID) {
				linked = domain.ID(cmd.MessageID)
			}
		}
		target, known := resolved[in.Target]
		if !known {
			target, err = reactionTarget(ctx, q, m.AccountID, m.ConversationID, in.Target)
			if err != nil {
				return out, err
			}
			resolved[in.Target] = target
		}
		if target != m.ID && linked != m.ID {
			continue
		}
		key := in.Actor
		if in.IsOwn {
			// The account has one own set even when the room changes how it
			// identifies participants.
			key = ""
		}
		head := reactionHead{observation.ID, observation.Ordinal, observation.ArchiveOrder, observation.Fingerprint, in}
		if prior, known := heads[key]; !known || head.after(prior) {
			heads[key] = head
		}
	}
	counts := map[string]domain.ReactionItem{}
	var latest int64
	for _, head := range heads {
		if head.ordinal > latest {
			latest = head.ordinal
			state.RevisionID = domain.ID(head.id)
		}
		if head.in.IsOwn {
			state.OwnRevisionID = domain.ID(head.id)
			state.OwnEmojis = append([]string{}, head.in.Emojis...)
		}
		if len(head.in.Emojis) > 0 {
			in := head.in
			if in.Kind == "room" && !publicRoom && in.IsOwn && !domain.IsRoomReactionActor(in.Actor) {
				in.Actor = domain.RoomReactionActorPrefix + bodyHash(string(in.AccountID)+"\x00"+in.Address+"\x00own")
				in.ActorName = in.Actor
			}
			actor, err := reactionActor(ctx, q, m.AccountID, in)
			if err != nil {
				return out, err
			}
			state.Actors = append(state.Actors, actor)
		}
		for _, emoji := range head.in.Emojis {
			item := counts[emoji]
			item.Emoji = emoji
			item.Count++
			item.Own = item.Own || head.in.IsOwn
			counts[emoji] = item
		}
	}
	slices.SortFunc(state.Actors, func(a, b domain.ReactionActor) int {
		if a.Own != b.Own {
			if a.Own {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Address, b.Address)
	})
	for _, item := range counts {
		state.Items = append(state.Items, item)
	}
	slices.SortFunc(state.Items, func(a, b domain.ReactionItem) int {
		if a.Emoji < b.Emoji {
			return -1
		}
		if a.Emoji > b.Emoji {
			return 1
		}
		return 0
	})
	if active, err := q.GetActiveMessageReaction(ctx, string(m.ID)); err == nil {
		p, err := decodeReaction(active)
		if err != nil {
			return out, err
		}
		state.PendingCommand = &p.Command
	} else if !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	out.Snapshot = state
	return out, nil
}

type reactionHead struct {
	id           string
	ordinal      int64
	archiveOrder int64
	fingerprint  string
	in           application.IncomingReaction
}

func (h reactionHead) after(prior reactionHead) bool {
	if cmp := h.in.CreatedAt.Compare(prior.in.CreatedAt); cmp != 0 {
		return cmp > 0
	}
	if (h.in.ArchiveID == "") != (prior.in.ArchiveID == "") {
		return h.in.ArchiveID != ""
	}
	if h.archiveOrder != prior.archiveOrder {
		return h.archiveOrder > prior.archiveOrder
	}
	if h.in.Kind == "room" && h.in.ArchiveID == "" && h.ordinal != prior.ordinal {
		return h.ordinal > prior.ordinal
	}
	if h.in.ProviderID != prior.in.ProviderID {
		return h.in.ProviderID > prior.in.ProviderID
	}
	return h.fingerprint > prior.fingerprint
}

func reactionActor(ctx context.Context, q *dbgen.Queries, account domain.ID, in application.IncomingReaction) (domain.ReactionActor, error) {
	actor := domain.ReactionActor{Address: in.Actor, DisplayName: strings.TrimSpace(in.ActorName), Own: in.IsOwn, Emojis: append([]string{}, in.Emojis...)}
	opaque := domain.IsRoomReactionActor(in.Actor)
	if !opaque {
		profile, err := q.ReactionActorProfile(ctx, dbgen.ReactionActorProfileParams{AccountID: string(account), Address: in.Actor})
		if err != nil {
			return actor, err
		}
		if name := strings.TrimSpace(profile.DisplayName); name != "" {
			actor.DisplayName = name
		}
		actor.AvatarHash = profile.AvatarHash
	}
	if actor.DisplayName == "" || len(actor.DisplayName) > 1023 || !utf8.ValidString(actor.DisplayName) || strings.IndexFunc(actor.DisplayName, unicode.IsControl) >= 0 {
		actor.DisplayName = actor.Address
		if opaque {
			return actor, application.ErrInvalid
		}
	}
	return actor, nil
}

func (s *Store) ReadMessageReactions(ctx context.Context, id domain.ID) (application.MessageReactionCandidate, error) {
	tx, q, err := begin(ctx, s)
	if err != nil {
		return application.MessageReactionCandidate{}, err
	}
	defer func() { _ = tx.Rollback() }()
	out, err := readReactions(ctx, q, id)
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}
func replayReaction(ctx context.Context, q *dbgen.Queries, id domain.ID, digest string) (*domain.MessageReactionCommand, error) {
	op, err := q.GetOperation(ctx, string(id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if op.Kind != "set_message_reactions" || op.Fingerprint != digest {
		return nil, application.ErrIdempotencyConflict
	}
	row, err := q.GetMessageReactionCommand(ctx, string(id))
	if err != nil {
		return nil, notFound(err)
	}
	p, err := decodeReaction(row)
	return &p.Command, err
}
func (s *Store) ReplayMessageReaction(ctx context.Context, id domain.ID, digest string) (*domain.MessageReactionCommand, error) {
	return replayReaction(ctx, s.queries, id, digest)
}
func (s *Store) MessageReactionCommand(ctx context.Context, id domain.ID) (domain.PendingMessageReaction, error) {
	row, err := s.queries.GetMessageReactionCommand(ctx, string(id))
	if err != nil {
		return domain.PendingMessageReaction{}, notFound(err)
	}
	return decodeReaction(row)
}
func checkReactionRoom(ctx context.Context, q *dbgen.Queries, p domain.PendingMessageReaction) error {
	row, err := q.GetConversation(ctx, string(p.Command.ConversationID))
	if err != nil {
		return err
	}
	c, err := decodeConversation(row)
	if err != nil {
		return err
	}
	if c.Kind != "room" {
		return nil
	}
	intent, err := q.GetRoomIntent(ctx, string(c.ID))
	if errors.Is(err, sql.ErrNoRows) {
		return application.ErrStateConflict
	}
	if err != nil {
		return err
	}
	if intent.Active == 0 || intent.RevisionID != string(p.Plan.RoomIntentRevisionID) {
		return application.ErrStateConflict
	}
	return nil
}
func writeReaction(ctx context.Context, q *dbgen.Queries, p domain.PendingMessageReaction, next time.Time) error {
	if err := q.UpdateMessageReactionCommand(ctx, dbgen.UpdateMessageReactionCommandParams{ID: string(p.Command.ID), RevisionID: string(p.Command.RevisionID), State: p.Command.State, Payload: jsonText(p), NextAttemptAt: stamp(next)}); err != nil {
		return err
	}
	return reactionEvent(ctx, q, p.Command.MessageID, p.Command.UpdatedAt)
}
func (s *Store) AcceptMessageReaction(ctx context.Context, in application.MessageReactionMutation) (application.MessageReactionResult, error) {
	tx, q, err := begin(ctx, s)
	if err != nil {
		return application.MessageReactionResult{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = q.AcquireWriteLock(ctx); err != nil {
		return application.MessageReactionResult{}, err
	}
	replay, err := replayReaction(ctx, q, in.Pending.Command.ID, in.Fingerprint)
	if err != nil {
		return application.MessageReactionResult{}, err
	}
	if replay != nil {
		return application.MessageReactionResult{Command: *replay, Replayed: true}, nil
	}
	p := in.Pending
	candidate, err := readReactions(ctx, q, p.Command.MessageID)
	if err != nil {
		return application.MessageReactionResult{}, err
	}
	if candidate.Snapshot.PendingCommand != nil || candidate.Snapshot.OwnRevisionID != p.Command.ExpectedOwnRevisionID || candidate.Source.ID != p.Command.MessageID || candidate.Source.AccountID != p.Command.AccountID || candidate.Source.ConversationID != p.Command.ConversationID || p.Plan.TargetMessageID != candidate.Source.ID {
		return application.MessageReactionResult{}, application.ReactionConflict{Current: candidate.Snapshot}
	}
	if err = checkReactionRoom(ctx, q, p); err != nil {
		return application.MessageReactionResult{}, err
	}
	account, err := q.GetAccount(ctx, string(p.Command.AccountID))
	if err != nil {
		return application.MessageReactionResult{}, err
	}
	var a domain.Account
	if err = json.Unmarshal([]byte(account), &a); err != nil {
		return application.MessageReactionResult{}, err
	}
	if p.Plan.Actor != a.Address || p.Plan.RoomActor != "" && !domain.IsRoomReactionActor(p.Plan.RoomActor) {
		return application.MessageReactionResult{}, application.ErrInvalid
	}
	if _, err = operation(ctx, q, application.Operation{ID: p.Command.ID, ResourceID: p.Command.ID, Kind: "set_message_reactions", CreatedAt: p.Command.CreatedAt}, in.Fingerprint, p.Command); err != nil {
		return application.MessageReactionResult{}, err
	}
	if err = q.InsertMessageReactionCommand(ctx, dbgen.InsertMessageReactionCommandParams{ID: string(p.Command.ID), RevisionID: string(p.Command.RevisionID), MessageID: string(p.Command.MessageID), AccountID: string(p.Command.AccountID), ConversationID: string(p.Command.ConversationID), State: p.Command.State, Payload: jsonText(p), NextAttemptAt: stamp(p.Command.CreatedAt)}); err != nil {
		return application.MessageReactionResult{}, err
	}
	if err = reactionEvent(ctx, q, p.Command.MessageID, p.Command.CreatedAt); err != nil {
		return application.MessageReactionResult{}, err
	}
	return application.MessageReactionResult{Command: p.Command}, tx.Commit()
}
func (s *Store) PendingMessageReactions(ctx context.Context, at time.Time) ([]domain.PendingMessageReaction, error) {
	rows, err := s.queries.PendingMessageReactionCommands(ctx, stamp(at))
	if err != nil {
		return nil, err
	}
	out := []domain.PendingMessageReaction{}
	for _, row := range rows {
		p, err := decodeReaction(row)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}
func (s *Store) InterruptedMessageReactions(ctx context.Context) ([]domain.PendingMessageReaction, error) {
	rows, err := s.queries.InterruptedMessageReactionCommands(ctx)
	if err != nil {
		return nil, err
	}
	out := []domain.PendingMessageReaction{}
	for _, row := range rows {
		p, err := decodeReaction(row)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}
func (s *Store) ClaimMessageReaction(ctx context.Context, id, revision domain.ID, at time.Time) (domain.PendingMessageReaction, error) {
	tx, q, err := begin(ctx, s)
	if err != nil {
		return domain.PendingMessageReaction{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = q.AcquireWriteLock(ctx); err != nil {
		return domain.PendingMessageReaction{}, err
	}
	row, err := q.GetMessageReactionCommand(ctx, string(id))
	if err != nil {
		return domain.PendingMessageReaction{}, notFound(err)
	}
	p, err := decodeReaction(row)
	if err != nil {
		return p, err
	}
	if p.Command.State != "queued" {
		return p, application.ErrStateConflict
	}
	candidate, err := readReactions(ctx, q, p.Command.MessageID)
	if err != nil {
		return p, err
	}
	p.Command.RevisionID = revision
	p.Command.UpdatedAt = at
	if candidate.Snapshot.OwnRevisionID != p.Command.ExpectedOwnRevisionID || checkReactionRoom(ctx, q, p) != nil {
		p.Command.State = "failed"
		p.Command.LastErrorCode = "state_conflict"
		if err = writeReaction(ctx, q, p, at); err != nil {
			return p, err
		}
		if err = tx.Commit(); err != nil {
			return p, err
		}
		return p, application.ErrStateConflict
	}
	p.Command.State = "dispatching"
	if err = writeReaction(ctx, q, p, at); err != nil {
		return p, err
	}
	return p, tx.Commit()
}
func insertReactionObservation(ctx context.Context, q *dbgen.Queries, in application.IncomingReaction, revision domain.ID, command domain.ID) (bool, error) {
	digest := bodyHash(jsonText([]any{in.Target.ID, in.Kind, in.Emojis}))
	matches, err := q.FindReactionObservation(ctx, dbgen.FindReactionObservationParams{
		AccountID: string(in.AccountID), Address: in.Address, Actor: in.Actor, ProviderID: in.ProviderID,
		ArchiveScope: in.ArchiveScope, ArchiveID: in.ArchiveID, CommandID: string(command), Fingerprint: digest,
	})
	if err != nil {
		return false, err
	}
	if len(matches) != 0 {
		return reconcileReactionObservation(ctx, q, in, command, matches)
	}
	var epoch int64
	if in.Kind == "room" && !in.IsOwn {
		privacy, err := getRoomReactionPrivacy(ctx, q, in.AccountID, in.Address)
		if err != nil {
			return false, err
		}
		epoch = privacy.Epoch
		// A newly discovered archived event from before a known mode change
		// must not reintroduce a peer set into the current identity mode.
		if epoch > 0 && in.ArchiveID != "" && stamp(in.CreatedAt) < privacy.ChangedAt {
			epoch--
		}
	}
	order, err := placeArchivedReaction(ctx, q, in, dbgen.MessageReactionObservation{})
	if err != nil {
		return false, err
	}
	err = q.InsertReactionObservation(ctx, dbgen.InsertReactionObservationParams{
		ID: string(revision), AccountID: string(in.AccountID), Address: in.Address, Kind: in.Kind, Actor: in.Actor,
		ProviderID: in.ProviderID, ArchiveScope: in.ArchiveScope, ArchiveID: in.ArchiveID,
		TargetID: in.Target.ID, TargetScope: in.Target.Scope, TargetAuthor: in.Target.Author, Fingerprint: digest,
		OccurredAt: stamp(in.CreatedAt), Payload: jsonText(in), CommandID: string(command), ArchiveOrder: order, RoomEpoch: epoch,
	})
	return err == nil, err
}

func reconcileReactionObservation(ctx context.Context, q *dbgen.Queries, in application.IncomingReaction, command domain.ID, matches []dbgen.MessageReactionObservation) (bool, error) {
	old := matches[0]
	for _, candidate := range matches {
		if old.ArchiveID == "" && candidate.ArchiveID != "" {
			old = candidate
		}
		if command == "" && candidate.CommandID != "" {
			command = domain.ID(candidate.CommandID)
		}
	}
	var merged application.IncomingReaction
	if err := json.Unmarshal([]byte(old.Payload), &merged); err != nil {
		return false, err
	}
	switch {
	case in.ArchiveID != "" && old.ArchiveID == "":
		merged = in
	case in.ArchiveID != "":
		merged.ProviderID = in.ProviderID
	case old.ArchiveID == "" && (old.ProviderID == string(command) ||
		strings.HasPrefix(old.ProviderID, "room-live:") && strings.HasPrefix(in.ProviderID, "room-reaction:")):
		merged.ProviderID = in.ProviderID
	}
	var order = old.ArchiveOrder
	if in.ArchiveID != "" {
		merged.ArchiveEarlier, merged.ArchiveLater, merged.ArchivePrepend = in.ArchiveEarlier, in.ArchiveLater, in.ArchivePrepend
		var err error
		order, err = placeArchivedReaction(ctx, q, merged, old)
		if err != nil {
			return false, err
		}
	}
	for _, duplicate := range matches {
		if duplicate.Ordinal != old.Ordinal {
			if err := q.DeleteReactionObservation(ctx, duplicate.Ordinal); err != nil {
				return false, err
			}
		}
	}
	if old.ProviderID == merged.ProviderID && old.ArchiveID == merged.ArchiveID &&
		old.ArchiveOrder == order && old.CommandID == string(command) {
		return len(matches) > 1, nil
	}
	// An echo or archive copy changes the evidence for the same logical
	// reaction. Keep the revision that subsequent own commands may rely on.
	err := q.ReconcileReactionObservation(ctx, dbgen.ReconcileReactionObservationParams{
		Ordinal: old.Ordinal, ProviderID: merged.ProviderID, ArchiveScope: merged.ArchiveScope, ArchiveID: merged.ArchiveID,
		OccurredAt: stamp(merged.CreatedAt), ArchiveOrder: order, Payload: jsonText(merged), CommandID: string(command),
	})
	return err == nil, err
}

func placeArchivedReaction(ctx context.Context, q *dbgen.Queries, in application.IncomingReaction, old dbgen.MessageReactionObservation) (int64, error) {
	if in.ArchiveID == "" {
		return 0, nil
	}
	if old.ArchiveID != "" && len(in.ArchiveEarlier) == 0 && len(in.ArchiveLater) == 0 {
		return old.ArchiveOrder, nil
	}
	at := stamp(in.CreatedAt)
	neighbors, err := q.ArchivedReactionNeighbors(ctx, dbgen.ArchivedReactionNeighborsParams{
		AccountID: string(in.AccountID), ArchiveScope: in.ArchiveScope, OccurredAt: at,
		Earlier: jsonText(in.ArchiveEarlier), Later: jsonText(in.ArchiveLater), ExcludedOrdinal: old.Ordinal,
	})
	if err != nil {
		return 0, err
	}
	earlier, later := neighbors.EarlierCount != 0, neighbors.LaterCount != 0
	if old.ArchiveID != "" && (!earlier || old.ArchiveOrder > neighbors.EarlierOrder) &&
		(!later || old.ArchiveOrder < neighbors.LaterOrder) {
		return old.ArchiveOrder, nil
	}
	// Prefer the neighbor already visited in this page's traversal when
	// older evidence disagrees. Subsequent events repair the other bound.
	order := neighbors.LastOrder + 1
	switch {
	case later && (in.ArchivePrepend || !earlier):
		order = neighbors.LaterOrder
	case earlier:
		order = neighbors.EarlierOrder + 1
	case in.ArchivePrepend:
		order = neighbors.FirstOrder - 1
	}
	err = q.ShiftArchivedReactions(ctx, dbgen.ShiftArchivedReactionsParams{
		AccountID: string(in.AccountID), ArchiveScope: in.ArchiveScope, OccurredAt: at,
		Placement: order, ExcludedOrdinal: old.Ordinal,
	})
	return order, err
}

func (s *Store) FinishMessageReaction(ctx context.Context, id domain.ID, state string, revision domain.ID, at, next time.Time, reason string) (domain.MessageReactionCommand, error) {
	tx, q, err := begin(ctx, s)
	if err != nil {
		return domain.MessageReactionCommand{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = q.AcquireWriteLock(ctx); err != nil {
		return domain.MessageReactionCommand{}, err
	}
	row, err := q.GetMessageReactionCommand(ctx, string(id))
	if err != nil {
		return domain.MessageReactionCommand{}, notFound(err)
	}
	p, err := decodeReaction(row)
	if err != nil {
		return p.Command, err
	}
	if p.Command.State != "dispatching" {
		return p.Command, application.ErrStateConflict
	}
	if state != "queued" && state != "submitted" && state != "unknown" && state != "failed" {
		return p.Command, application.ErrInvalid
	}
	if state == "submitted" {
		candidate, err := readReactions(ctx, q, p.Command.MessageID)
		if err != nil {
			return p.Command, err
		}
		if candidate.Snapshot.OwnRevisionID == p.Command.ExpectedOwnRevisionID {
			crow, err := q.GetConversation(ctx, string(p.Command.ConversationID))
			if err != nil {
				return p.Command, err
			}
			c, err := decodeConversation(crow)
			if err != nil {
				return p.Command, err
			}
			_, err = insertReactionObservation(ctx, q, application.IncomingReaction{AccountID: p.Command.AccountID, Address: c.Address, Kind: c.Kind, ProviderID: string(p.Command.ID), CreatedAt: at, IsOwn: true, Actor: p.Plan.ObservationActor(), ActorName: p.Plan.ActorName, Target: p.Plan.Reference, Emojis: p.Command.Emojis}, revision, p.Command.ID)
			if err != nil {
				return p.Command, err
			}
		}
	}
	p.Command.State = state
	p.Command.LastErrorCode = reason
	p.Command.RevisionID = revision
	p.Command.UpdatedAt = at
	if err = writeReaction(ctx, q, p, next); err != nil {
		return p.Command, err
	}
	return p.Command, tx.Commit()
}
func (s *Store) ObserveMessageReaction(ctx context.Context, in application.MessageReactionObservation) error {
	tx, q, err := begin(ctx, s)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = q.AcquireWriteLock(ctx); err != nil {
		return err
	}
	value := in.Incoming
	var command domain.ID
	if value.IsOwn {
		echoID := value.ProviderID
		if value.CommandEchoID != "" {
			echoID = value.CommandEchoID
		}
		row, lookup := q.GetMessageReactionCommand(ctx, echoID)
		if lookup == nil {
			p, err := decodeReaction(row)
			if err != nil {
				return err
			}
			if p.Command.AccountID == value.AccountID && p.Plan.ObservationActor() == value.Actor &&
				p.Plan.Reference.ID == value.Target.ID && (value.Kind != "room" || p.Plan.Reference.Scope == value.Target.Scope) &&
				reflect.DeepEqual(p.Command.Emojis, value.Emojis) {
				command = p.Command.ID
				if p.Command.State == "queued" || p.Command.State == "dispatching" || p.Command.State == "unknown" {
					p.Command.State = "submitted"
					p.Command.LastErrorCode = ""
					p.Command.RevisionID = in.RevisionID
					p.Command.UpdatedAt = in.ObservedAt
					if err = writeReaction(ctx, q, p, in.ObservedAt); err != nil {
						return err
					}
				}
			}
		} else if !errors.Is(lookup, sql.ErrNoRows) {
			return lookup
		}
	}
	changed, err := insertReactionObservation(ctx, q, value, in.RevisionID, command)
	if err != nil {
		return err
	}
	if changed {
		crow, err := q.FindConversation(ctx, dbgen.FindConversationParams{AccountID: string(value.AccountID), Address: value.Address})
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil {
			target, err := reactionTarget(ctx, q, value.AccountID, domain.ID(crow.ID), value.Target)
			if err != nil {
				return err
			}
			if target != "" {
				if err = reactionEvent(ctx, q, target, in.ObservedAt); err != nil {
					return err
				}
			}
		}
	}
	return tx.Commit()
}
func (s *Store) RejectMessageReaction(ctx context.Context, id, revision domain.ID, at time.Time) error {
	tx, q, err := begin(ctx, s)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = q.AcquireWriteLock(ctx); err != nil {
		return err
	}
	row, err := q.GetMessageReactionCommand(ctx, string(id))
	if err != nil {
		return notFound(err)
	}
	p, err := decodeReaction(row)
	if err != nil {
		return err
	}
	if p.Command.State == "failed" {
		return nil
	}
	p.Command.State = "failed"
	p.Command.LastErrorCode = "protocol_rejected"
	p.Command.RevisionID = revision
	p.Command.UpdatedAt = at
	if err = writeReaction(ctx, q, p, at); err != nil {
		return err
	}
	return tx.Commit()
}

func getRoomReactionPrivacy(ctx context.Context, q *dbgen.Queries, account domain.ID, address string) (dbgen.GetRoomReactionPrivacyRow, error) {
	row, err := q.GetRoomReactionPrivacy(ctx, dbgen.GetRoomReactionPrivacyParams{AccountID: string(account), Address: address})
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return row, err
}

func (s *Store) SetRoomReactionPrivacy(ctx context.Context, account domain.ID, address string, privacy application.RoomReactionPrivacy) error {
	tx, q, err := begin(ctx, s)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	now := time.Now().UTC()
	var visibility int64
	if privacy.Public && privacy.Mode == application.RoomReactionsNonanonymous {
		visibility = 1
	}
	changed, err := q.SetRoomReactionPrivacy(ctx, dbgen.SetRoomReactionPrivacyParams{
		AccountID: string(account), Address: address, Public: visibility,
		IdentityMode: privacy.Mode, ChangedAt: stamp(now),
	})
	if err != nil {
		return err
	}
	if changed > 0 {
		row, err := q.FindConversation(ctx, dbgen.FindConversationParams{AccountID: string(account), Address: address})
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil {
			conversation, err := decodeConversation(row)
			if err != nil {
				return err
			}
			if conversation.Kind != "room" {
				return tx.Commit()
			}
			if _, err = change(ctx, q, "conversation.updated", conversation, now); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}
