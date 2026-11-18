package sqlite

import (
	"errors"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func TestExpiredQuoteDropsOnlyPreviewsForRemovedURLs(t *testing.T) {
	source, engine, chat := outboxFixture(t)
	ctx := t.Context()
	now := time.Now().UTC()
	const privateURL = "https://example.invalid/expired-quote"
	const retainedURL = "https://example.invalid/retained-link"
	const quote = "> " + privateURL + " " + retainedURL + "\n"
	const answer = "retained reply " + retainedURL
	ref := domain.MessageReference{Scope: "sender:bob@localhost", Author: "bob@localhost", ID: "expired-preview-quote"}
	reply := replySeed(t, source, chat, quote+answer, now, nil, nil)
	previews, err := engine.ConversationPreviews(ctx, chat.ID, []domain.ID{reply.ID})
	if err != nil || len(previews) != 2 || previews[0].URL != privateURL || previews[1].URL != retainedURL {
		t.Fatalf("preview fixture did not reproduce before learning reply metadata: %+v, %v", previews, err)
	}
	private, retained := previews[0], previews[1]
	privateContent := dbgen.PutSharedContentParams{
		ID: string(migrationID()), RevisionID: string(migrationID()), MessageID: string(reply.ID), Url: privateURL, Kind: "link",
	}
	if err := source.queries.PutSharedContent(ctx, privateContent); err != nil {
		t.Fatal(err)
	}
	destination := copyRestoreFixture(t, source)
	local := retained
	local.RevisionID, local.State, local.Title = migrationID(), "ready", "retained destination preview"
	if err := destination.SavePreview(ctx, local, retained.RevisionID); err != nil {
		t.Fatal(err)
	}
	replySeed(t, source, chat, privateURL, now.AddDate(0, 0, -9), []domain.MessageReference{ref}, nil)
	reply.ReplyReference = &domain.ReplyReference{Target: ref, Fallback: []domain.TextRange{{Start: 0, End: len(quote)}}}
	if err := source.ReceiveMessage(ctx, chat, reply, migrationID()); err != nil {
		t.Fatal(err)
	}
	days := 7
	if _, err := engine.SetConversationRetention(ctx, chat.ID, "0", &days); err != nil {
		t.Fatal(err)
	}
	if count, err := source.PurgeExpiredHistory(ctx, now, func() (domain.ID, error) { return migrationID(), nil }); err != nil || count != 1 {
		t.Fatalf("purge fixture: %d, %v", count, err)
	}
	check := func(t *testing.T, store *Store, kept domain.MessagePreview) {
		t.Helper()
		checker := testRestoreEngine(t, store)
		body, err := checker.Message(ctx, reply.ID)
		if err != nil || body.OriginalText != answer || body.Reply == nil || !body.Reply.OriginalExpired {
			t.Fatalf("retained reply was not redacted: %+v, %v", body, err)
		}
		if prior, err := checker.Preview(ctx, private.ID); !errors.Is(err, application.ErrNotFound) {
			t.Fatalf("expired quote preview remains accessible: %+v, %v", prior, err)
		}
		if prior, err := checker.Preview(ctx, kept.ID); err != nil || prior.RevisionID != kept.RevisionID || prior.URL != retainedURL || prior.Title != kept.Title {
			t.Fatalf("retained URL lost its preview revision: %+v, %v", prior, err)
		}
		jobs, err := store.PreviewJobs(ctx, now.Add(time.Minute), 20)
		if err != nil {
			t.Fatal(err)
		}
		for _, job := range jobs {
			if job.ID == private.ID {
				t.Fatalf("expired quote URL is still scheduled for fetch: %+v", job)
			}
		}
		events, err := store.Events(ctx, 0, 100)
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range events {
			if event.Preview != nil && event.Preview.ID == private.ID {
				t.Fatalf("expired quote is still present in preview history: %+v", event.Preview)
			}
		}
		var hasPrivateReference, hasPrivateCaption bool
		if err := store.readDB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM shared_content WHERE message_id=? AND url=?),
			EXISTS(SELECT 1 FROM shared_content_state WHERE message_id=? AND instr(caption,?)>0)`,
			reply.ID, privateURL, reply.ID, privateURL).Scan(&hasPrivateReference, &hasPrivateCaption); err != nil || hasPrivateReference || hasPrivateCaption {
			t.Fatalf("quote remains in shared content reference or caption: %t, %t, %v", hasPrivateReference, hasPrivateCaption, err)
		}
	}
	t.Run("live purge", func(t *testing.T) { check(t, source, retained) })

	// Older executables could write a redacted message while leaving its
	// previously cached private preview in the archive.
	if _, err := source.EnsurePreview(ctx, private); err != nil {
		t.Fatal(err)
	}
	if err := source.queries.PutSharedContent(ctx, privateContent); err != nil {
		t.Fatal(err)
	}
	if err := source.queries.MarkSharedContentIndexed(ctx, dbgen.MarkSharedContentIndexedParams{
		MessageID: string(reply.ID), ParserVersion: application.SharedContentVersion, Caption: quote + answer,
	}); err != nil {
		t.Fatal(err)
	}
	manifest, archive, scratch := backupRestoreFixture(t, source, false)
	t.Run("existing destination", func(t *testing.T) {
		if _, err := destination.mergeBackup(ctx, archive, scratch, manifest, now); err != nil {
			t.Fatal(err)
		}
		check(t, destination, local)
		if _, err := destination.mergeBackup(ctx, archive, scratch, manifest, now); err != nil {
			t.Fatal(err)
		}
		check(t, destination, local)
		events, err := destination.Events(ctx, 0, 100)
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range events {
			if event.Preview != nil && event.Preview.RevisionID == local.RevisionID {
				return
			}
		}
		t.Fatal("restore removed the destination event for the retained preview")
	})
	t.Run("fresh destination", func(t *testing.T) {
		fresh, err := Open(ctx, t.TempDir(), migrationID())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = fresh.Close() })
		if _, err := fresh.mergeBackup(ctx, archive, scratch, manifest, now); err != nil {
			t.Fatal(err)
		}
		check(t, fresh, retained)
		if _, err := fresh.mergeBackup(ctx, archive, scratch, manifest, now); err != nil {
			t.Fatal(err)
		}
		check(t, fresh, retained)
	})
}
