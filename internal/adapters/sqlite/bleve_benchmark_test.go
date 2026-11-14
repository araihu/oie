package sqlite

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/domain"
)

var indexBenchRows = flag.Int("oie-index-bench-rows", 100000, "retained messages in the experimental index measurement")

type chronologicalIndexControl struct{ *Store }

func (s chronologicalIndexControl) SearchMessages(ctx context.Context, p domain.MessageSearchQuery) (domain.MessageSearchPage, error) {
	return s.searchMessages(ctx, p, beginRead, func(ctx context.Context, _ *sql.Tx, q *dbgen.Queries, p dbgen.SearchMessagesParams) ([]dbgen.SearchMessagesRow, error) {
		return searchChronological(ctx, q, p)
	})
}

func seedIndexBenchmark(t *testing.T, s *Store, c domain.Conversation, size int) {
	t.Helper()
	const insert = `WITH RECURSIVE seq(n) AS (VALUES(1) UNION ALL SELECT n+1 FROM seq WHERE n<?1),
 seed AS (
 SELECT n,printf('019cd000-0000-7000-8000-%012x',n) id,printf('019cd000-0000-7000-8001-%012x',n) rid,
 CASE WHEN n%1000=0 THEN 'O arquivo do contrato tem prazo em novembro'
 WHEN n%4=0 THEN 'Recebi o pagamento e confirmei o faturamento para a semana'
 ELSE 'O vídeo está pronto e vou mandar a versão amanhã' END || ' ' || n ||
 CASE WHEN n%1777=0 THEN ' documento anexo' ELSE '' END AS body
 FROM seq
 )
 INSERT INTO messages(id,revision_id,account_id,conversation_id,provider_id,is_own,state,payload,created_at_ms)
 SELECT id,rid,?2,?3,'',0,'received',json_object('ID',id,'RevisionID',rid,'AccountID',?2,
 'ConversationID',?3,'Sender',json_object('ID',?4,'DisplayName','Peer'),'CreatedAt',?5,
 'OriginalText',body,'SendState','received'),1700000000000+n FROM seed`
	ctx := t.Context()
	tx, err := s.db.BeginTx(ctx, nil)
	requireIndexTest(t, err)
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, insert, size, c.AccountID, c.ID, c.PeerID, stamp(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)))
	requireIndexTest(t, err)
	_, err = tx.ExecContext(ctx, "INSERT INTO message_revisions SELECT revision_id,id,'[]',payload,? FROM messages", stamp(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)))
	requireIndexTest(t, err)
	requireIndexTest(t, tx.Commit())
	_, err = s.db.ExecContext(ctx, "ANALYZE")
	requireIndexTest(t, err)
}

type indexMeasurement struct {
	latency time.Duration
	err     error
}

func indexP95(results []time.Duration) time.Duration {
	slices.Sort(results)
	return results[int(math.Ceil(float64(len(results))*.95))-1]
}

func TestExperimentalMessageIndexIngestionBenchmark(t *testing.T) {
	if os.Getenv("OIE_BLEVE_BENCH") != "1" {
		t.Skip("set OIE_BLEVE_BENCH=1 to measure concurrent ingest")
	}
	// Finish the initial build before either timed arm. This keeps the two
	// measurement windows close together on a shared host.
	type fixture struct {
		mode         string
		store        *Store
		conversation domain.Conversation
	}
	fixtures := make([]fixture, 0, 2)
	for _, mode := range []string{"sql-read", "bleve"} {
		old, _, c := outboxFixture(t)
		seedIndexBenchmark(t, old, c, *indexBenchRows)
		dir := old.dir
		requireIndexTest(t, old.Close())

		var options []Option
		if mode == "bleve" {
			options = append(options, WithExperimentalSearch())
		}
		startBuild := time.Now()
		store, err := Open(t.Context(), dir, migrationID(), options...)
		requireIndexTest(t, err)
		t.Cleanup(func() { _ = store.Close() })
		if mode == "bleve" {
			waitIndexUntil(t, store, 8*time.Minute, func(state dbgen.MessageSearchIndex) bool {
				return indexIsReady(store, state, "")
			})
			t.Logf("background index ready after %s", time.Since(startBuild))
		}
		fixtures = append(fixtures, fixture{mode, store, c})
	}
	for _, f := range fixtures {
		mode, store, c := f.mode, f.store, f.conversation
		t.Run(mode, func(t *testing.T) {
			var serverURL string
			if mode == "bleve" {
				serverURL = openIndexTestHTTP(t, store).URL
			} else {
				serverURL = openIndexTestHTTP(t, chronologicalIndexControl{store}).URL
			}
			client := &http.Client{Timeout: 90 * time.Second}
			t.Cleanup(client.CloseIdleConnections)
			terms := []string{"arqiuvo", "docun", "faturamento", "viedo"}
			addresses := make([]string, len(terms))
			for n, term := range terms {
				addresses[n] = serverURL + "/api/v1/messages/search?" + (url.Values{"q": {term}, "limit": {"20"}}).Encode()
				requireIndexTest(t, measureIndexRequest(t.Context(), client, addresses[n]))
			}

			// Fixed offered rates. Each lane queues work and includes time spent
			// waiting behind earlier requests. The writer uses actual archive
			// message acceptance and commits all offered messages.
			const queries = 120
			const writes = 240
			const queryInterval = 100 * time.Millisecond
			const writeInterval = 50 * time.Millisecond
			type job struct {
				n  int
				at time.Time
			}
			queryJobs, writeJobs := make(chan job, queries), make(chan job, writes)
			writeResults := make(chan indexMeasurement, writes)
			queryResults := make(chan struct {
				index  int
				result indexMeasurement
			}, queries)
			start := time.Now().Add(100 * time.Millisecond)
			var workers sync.WaitGroup
			var lastWrite time.Time
			var archiveIDs []string
			peakPending := 0
			for range 4 {
				workers.Go(func() {
					for task := range queryJobs {
						n := task.n % len(terms)
						err := measureIndexRequest(t.Context(), client, addresses[n])
						queryResults <- struct {
							index  int
							result indexMeasurement
						}{n, indexMeasurement{time.Since(task.at), err}}
					}
				})
			}
			workers.Go(func() {
				for task := range writeJobs {
					at := time.Now().UTC()
					m := domain.Message{ID: migrationID(), RevisionID: migrationID(), AccountID: c.AccountID, ConversationID: c.ID,
						Sender: domain.Sender{ID: c.PeerID, DisplayName: "Peer"}, OriginalText: fmt.Sprintf("ingestão concorrente concorrenciaverificada %d", task.n),
						CreatedAt: at.Add(-time.Hour), Historical: true, SendState: "received", ArchiveID: fmt.Sprintf("archive-bench-%d", task.n), ArchiveScope: c.Address}
					receipt := c
					receipt.UpdatedAt = at
					err := store.ReceiveMessage(t.Context(), receipt, m, migrationID())
					lastWrite = time.Now()
					writeResults <- indexMeasurement{lastWrite.Sub(task.at), err}
					archiveIDs = append(archiveIDs, m.ArchiveID)
					if mode == "bleve" && task.n%10 == 0 {
						pending, err := store.read.ListMessageSearchPending(t.Context(), searchCandidateLimit+1)
						if err == nil {
							peakPending = max(peakPending, len(pending))
						}
					}
				}
			})
			var used, crossed, failed uint64
			if mode == "bleve" {
				used = store.messageIndex.used.Load()
				crossed = store.messageIndex.crossedBatch.Load()
				failed = store.messageIndex.lookupFallback.Load()
			}
			var arrivals sync.WaitGroup
			for _, lane := range []struct {
				jobs     chan job
				count    int
				interval time.Duration
			}{{queryJobs, queries, queryInterval}, {writeJobs, writes, writeInterval}} {
				arrivals.Go(func() {
					defer close(lane.jobs)
					for n := range lane.count {
						at := start.Add(time.Duration(n) * lane.interval)
						time.Sleep(time.Until(at))
						lane.jobs <- job{n, at}
					}
				})
			}
			arrivals.Wait()
			workers.Wait()
			elapsed := time.Since(start)
			close(queryResults)
			close(writeResults)
			var readTimes [4][]time.Duration
			for result := range queryResults {
				requireIndexTest(t, result.result.err)
				readTimes[result.index] = append(readTimes[result.index], result.result.latency)
			}
			var writeTimes []time.Duration
			for result := range writeResults {
				requireIndexTest(t, result.err)
				writeTimes = append(writeTimes, result.latency)
			}
			if len(writeTimes) != writes {
				t.Fatalf("committed %d out of %d offered messages", len(writeTimes), writes)
			}
			for n, samples := range readTimes {
				if len(samples) != queries/len(terms) {
					t.Fatalf("completed %d queries for %s", len(samples), terms[n])
				}
				t.Logf("%s %s HTTP p95=%s n=%d", mode, terms[n], indexP95(samples), len(samples))
			}
			t.Logf("%s archive ingest p95=%s committed=%d elapsed=%s", mode, indexP95(writeTimes), writes, elapsed)
			retained := make(map[string]bool, len(archiveIDs))
			for _, archiveID := range archiveIDs {
				id, err := store.read.GetArchiveAlias(t.Context(), dbgen.GetArchiveAliasParams{AccountID: string(c.AccountID), Scope: c.Address, ArchiveID: archiveID})
				requireIndexTest(t, err)
				if retained[id] {
					t.Fatalf("archive message %s reused retained ID %s", archiveID, id)
				}
				retained[id] = true
			}
			if mode == "bleve" {
				t.Logf("during load: index used=%d, crossed a batch=%d, lookup declined=%d",
					store.messageIndex.used.Load()-used, store.messageIndex.crossedBatch.Load()-crossed, store.messageIndex.lookupFallback.Load()-failed)
				waitIndex(t, store, func(dbgen.MessageSearchIndex) bool {
					pending, err := store.read.ListMessageSearchPending(t.Context(), 1)
					return err == nil && len(pending) == 0
				})
				t.Logf("pending=%d peak sampled (cap %d); drain observed within %s of final write", peakPending, searchCandidateLimit+1, time.Since(lastWrite))
				checkIndexParity(t, store, domain.MessageSearchQuery{Search: "concorrenciaverificada", Limit: 50})
			}
		})
	}
}
