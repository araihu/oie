package sqlite

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/search"
	"github.com/blevesearch/bleve/v2"
)

const (
	searchProjectionVersion     = 1
	searchIndexBatchSize        = 64
	searchIndexPoll             = 500 * time.Millisecond
	searchIndexMaxDocumentBytes = 16 << 10
)

var (
	searchIndexCheckpointKey = []byte("oie.search.batch")
	searchIndexEngineKey     = []byte("oie.search.engine")
)

type messageIndex struct {
	store *Store
	// Readers compare the active generation and epoch on both sides of candidate
	// selection. Neither SQLite nor Scorch I/O holds this lock.
	gate           sync.RWMutex
	active         *messageIndexGeneration
	epoch          uint64
	cancel         context.CancelFunc
	done           chan struct{}
	used           atomic.Uint64
	crossedBatch   atomic.Uint64
	lookupFallback atomic.Uint64
}

type messageIndexGeneration struct {
	index bleve.Index
	state dbgen.MessageSearchIndex
}

type messageIndexBatch struct {
	ids     []string
	docs    map[string]dbgen.ListMessageSearchDocumentsRow
	pending []dbgen.MessageSearchPending
	cursor  string
	ready   int64
}

func (s *Store) startMessageIndex() {
	ctx, cancel := context.WithCancel(context.Background())
	worker := &messageIndex{store: s, cancel: cancel, done: make(chan struct{})}
	s.messageIndex = worker
	go worker.run(ctx)
}

func (i *messageIndex) run(ctx context.Context) {
	defer close(i.done)
	for ctx.Err() == nil {
		err := i.maintain(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			slog.Warn("experimental message index unavailable", "error", err)
		}
		waitSearchWorker(ctx, time.Second)
	}
}

func (i *messageIndex) maintain(ctx context.Context) error {
	generation, err := i.open(ctx)
	if err != nil {
		return err
	}
	defer func() {
		i.gate.Lock()
		i.active = nil
		i.gate.Unlock()
		_ = generation.index.Close()
	}()
	if generation.state.Ready != 0 {
		i.gate.Lock()
		i.active = generation
		i.gate.Unlock()
	}
	for ctx.Err() == nil {
		work, err := i.readBatch(ctx, generation.state)
		if err != nil {
			return err
		}
		if len(work.ids) == 0 && work.ready == generation.state.Ready {
			waitSearchWorker(ctx, searchIndexPoll)
			continue
		}
		if err := i.publish(ctx, generation, work); err != nil {
			return err
		}
		// Yield the writer and scheduler between batches of archive ingestion.
		waitSearchWorker(ctx, 10*time.Millisecond)
	}
	return ctx.Err()
}

func waitSearchWorker(ctx context.Context, interval time.Duration) {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

func messageIndexToken() string {
	var bytes [16]byte
	rand.Read(bytes[:])
	return hex.EncodeToString(bytes[:])
}

func validIndexGeneration(name string) bool {
	decoded, err := hex.DecodeString(name)
	return err == nil && len(decoded) == 16
}

func (i *messageIndex) open(ctx context.Context) (*messageIndexGeneration, error) {
	dir := filepath.Join(i.store.dir, "private", "message-search")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	engineID, _, err := i.store.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	state, err := i.store.read.GetMessageSearchIndex(ctx)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if state.ProjectionVersion == searchProjectionVersion && validIndexGeneration(state.Generation) {
		idx, err := bleve.Open(filepath.Join(dir, state.Generation))
		if err == nil {
			marker, err := idx.GetInternal(searchIndexCheckpointKey)
			owner, ownerErr := idx.GetInternal(searchIndexEngineKey)
			if err == nil && ownerErr == nil && string(owner) == string(engineID) && string(marker) == state.Batch && state.Batch != "" {
				return &messageIndexGeneration{index: idx, state: state}, nil
			}
			_ = idx.Close()
		}
	}
	return i.rebuild(ctx, dir, string(engineID))
}

func (i *messageIndex) rebuild(ctx context.Context, dir, engineID string) (*messageIndexGeneration, error) {
	// The caller owns the engine lock. Remove retired or interrupted sidecars
	// before allocating another index. No reader is using a previous generation.
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if validIndexGeneration(entry.Name()) {
			if err := os.RemoveAll(filepath.Join(dir, entry.Name())); err != nil {
				return nil, err
			}
		}
	}
	state := dbgen.MessageSearchIndex{Generation: messageIndexToken(), ProjectionVersion: searchProjectionVersion, Batch: messageIndexToken()}
	idx, err := newMessageIndex(filepath.Join(dir, state.Generation))
	if err != nil {
		return nil, err
	}
	if err := i.initialize(ctx, idx, state, engineID); err != nil {
		return nil, errors.Join(err, idx.Close())
	}
	return &messageIndexGeneration{index: idx, state: state}, nil
}

func (i *messageIndex) initialize(ctx context.Context, idx bleve.Index, state dbgen.MessageSearchIndex, engineID string) error {
	batch := idx.NewBatch()
	batch.SetInternal(searchIndexCheckpointKey, []byte(state.Batch))
	batch.SetInternal(searchIndexEngineKey, []byte(engineID))
	if err := idx.Batch(batch); err != nil {
		return err
	}
	tx, q, err := begin(ctx, i.store)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	// Clearing the old queue is safe when the new cursor starts from scratch.
	// From this transaction on, triggers cover writes anywhere in the scan.
	if err = q.StartMessageSearchIndex(ctx, dbgen.StartMessageSearchIndexParams{
		Generation: state.Generation, ProjectionVersion: state.ProjectionVersion, Batch: state.Batch,
	}); err != nil {
		return err
	}
	if err = q.ClearMessageSearchPending(ctx); err != nil {
		return err
	}
	if err = q.ClearMessageSearchExceptions(ctx); err != nil {
		return err
	}
	return tx.Commit()
}

func (i *messageIndex) readBatch(ctx context.Context, state dbgen.MessageSearchIndex) (messageIndexBatch, error) {
	work := messageIndexBatch{cursor: state.Cursor, ready: state.Ready}
	tx, q, err := beginRead(ctx, i.store)
	if err != nil {
		return work, err
	}
	defer func() { _ = tx.Rollback() }()
	if state.Ready == 0 {
		work.ids, err = q.ListMessageSearchBuildIDs(ctx, dbgen.ListMessageSearchBuildIDsParams{Cursor: state.Cursor, BatchLimit: searchIndexBatchSize})
		if err != nil {
			return work, err
		}
		if len(work.ids) == 0 {
			work.ready = 1
			return work, nil
		}
		work.cursor = work.ids[len(work.ids)-1]
	} else {
		work.pending, err = q.ListMessageSearchPending(ctx, searchIndexBatchSize)
		if err != nil {
			return work, err
		}
		for _, pending := range work.pending {
			work.ids = append(work.ids, pending.MessageID)
		}
		if len(work.ids) == 0 {
			return work, nil
		}
	}
	rows, err := q.ListMessageSearchDocuments(ctx, jsonText(work.ids))
	if err != nil {
		return work, err
	}
	work.docs = make(map[string]dbgen.ListMessageSearchDocumentsRow, len(rows))
	for _, row := range rows {
		work.docs[row.ID] = row
	}
	return work, nil
}

func (i *messageIndex) publish(ctx context.Context, g *messageIndexGeneration, work messageIndexBatch) error {
	batch := g.index.NewBatch()
	exceptions := make(map[string]bool)
	for _, id := range work.ids {
		row, found := work.docs[id]
		if !found {
			batch.Delete(id)
			continue
		}
		// Oversized and malformed legacy documents remain directly searchable.
		if len(row.Body)+len(row.Metadata)+len(row.History) > searchIndexMaxDocumentBytes ||
			!utf8.ValidString(row.Body) || !utf8.ValidString(row.Metadata) || !utf8.ValidString(row.History) {
			batch.Delete(id)
			exceptions[id] = true
			continue
		}
		folded := search.Fold(row.Body + " " + row.Metadata + " " + row.History)
		if len(folded) > searchIndexMaxDocumentBytes {
			batch.Delete(id)
			exceptions[id] = true
			continue
		}
		prefixes, grams := messageIndexTokens(folded)
		if err := batch.Index(id, map[string]any{
			"prefix": prefixes, "gram": grams, "account": row.AccountID, "conversation": row.ConversationID,
		}); err != nil {
			return err
		}
	}
	marker := messageIndexToken()
	batch.SetInternal(searchIndexCheckpointKey, []byte(marker))

	if err := ctx.Err(); err != nil {
		return err
	}
	// A read crossing this boundary could pair an older SQLite snapshot with
	// changed postings. A read starting after the boundary sees pending IDs
	// for this batch or starts after its durable acknowledgement.
	i.gate.Lock()
	i.epoch++
	i.gate.Unlock()
	err := g.index.Batch(batch)
	if err == nil {
		err = i.acknowledge(ctx, g.state, marker, work, exceptions)
	}
	i.gate.Lock()
	defer i.gate.Unlock()
	if err != nil {
		i.active = nil
		return err
	}
	g.state.Batch, g.state.Cursor, g.state.Ready = marker, work.cursor, work.ready
	if work.ready != 0 {
		i.active = g
	}
	return nil
}

func (i *messageIndex) acknowledge(ctx context.Context, state dbgen.MessageSearchIndex, marker string, work messageIndexBatch, exceptions map[string]bool) error {
	tx, q, err := begin(ctx, i.store)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	changed, err := q.AdvanceMessageSearchIndex(ctx, dbgen.AdvanceMessageSearchIndexParams{
		Batch: marker, Cursor: work.cursor, Ready: work.ready, Generation: state.Generation, PreviousBatch: state.Batch,
	})
	if err != nil {
		return err
	}
	if changed != 1 {
		return errors.New("message search checkpoint changed")
	}
	for _, id := range work.ids {
		if exceptions[id] {
			err = q.SetMessageSearchException(ctx, id)
		} else {
			err = q.RemoveMessageSearchException(ctx, id)
		}
		if err != nil {
			return err
		}
	}
	for _, pending := range work.pending {
		if err := q.AcknowledgeMessageSearchPending(ctx, dbgen.AcknowledgeMessageSearchPendingParams(pending)); err != nil {
			return err
		}
	}
	return tx.Commit()
}
