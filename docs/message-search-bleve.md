# Bleve for retained message search

**RECOMMENDED R08 · 2026-11-14.** Evaluate Bleve with Scorch as the
engine's derived index for global and conversation message search. The HTTP
contract, retained-message semantics and chronological cursor are the
compatibility baseline. The prototype measured 33.8 ms p95 for the first
global page of `docun` → `documento`, where the proposed FTS5 trigram plan
fell back to SQL at 1,479 ms. Both indexes accelerated selective queries
they could cover, with the lead varying by page. Small rooms and common
terms favored the chronological SQL path. The prototype supports advancing
the Bleve design and leaves the production choice open until write and
recovery costs are measured. [Benchmark and reproduction](message-search-index-benchmark.md)

## Engine and query boundary

Run one active Scorch generation per engine under its private data
directory, inside the Go runtime that owns SQLite. The engine owns the
worker, index handle and shutdown order. The SQLite-backed search
repository coordinates candidate selection and verification behind the
existing application port. Scope
the index to the selected engine and use account and conversation IDs
as candidate filters. Global conversation and contact results, the
shared-content catalog and remote XMPP archive search retain their
existing paths in the first increment. [Current message search](message-search.md)

Project one document per canonical message, keyed by its UUID. Index
normalized literal trigram and fuzzy prefix tokens derived from current
text, available searchable metadata and eligible retained content
revisions. Keep the source of each field and revision available in
SQLite. The union of versions may broaden the candidate set, so verify
each eligible revision independently. Keep the existing matcher in the
SQLite read path and only hydrate bounded, verified hits. It checks
account, conversation, visibility, content revision and text or metadata
before applying the chronological cursor and page limit. Keep Bleve
scores out of the cursor. [Current verification and ordering](../internal/adapters/sqlite/queries/message_search.sql)

Use the chronological SQL path for small scopes, dense or unsupported
candidate queries, index errors and incomplete index coverage that
cannot be enumerated cheaply. Limit candidate enumeration with a
threshold plus one and fall back on overflow or partial responses.
Include pending IDs and projection exceptions in the candidate set
before applying the requested limit. Search over the engine's retained data and validate queries as
today. [Prototype candidate rules](../internal/adapters/sqlite/search_prototype_test.go)

## Build, update and recovery

1. **Open.** Open and migrate SQLite under the existing engine data
   directory lock. Validate an existing index against the SQLite
   generation, engine identity and projection version. The projection
   version covers the searchable fields, parser, normalization and
   mapping. Start serving searches via SQL whenever a valid generation
   is unavailable. A missing or corrupt index can be rebuilt from local
   data while the engine serves requests.
2. **Build.** Register changes in SQLite before starting the initial
   scan. Build a new generation in the background in bounded batches over
   canonical messages, including previously retained or restored history.
   Use a resumable keyset scan and replay changes made while scanning.
   Maintain a durable checkpoint for the building generation and retain
   its changes independently of acknowledgements by the active index.
   Publish only after every eligible record is indexed or covered by a
   durable pending or exception row for the new generation. Resume the
   build from durable checkpoints on compatible restarts. Rebuild when
   the projection version changes. A previous generation may serve
   during rebuild if it still meets the active search contract and
   continues to receive updates.
3. **Update.** Write or refresh a pending canonical UUID and mutation
   version in the same SQLite transaction as each searchable change.
   Cover live and archive ingestion, imports, edits, old and new roots
   after alias changes, removals, and changes to references or cached
   previews. The worker coalesces by UUID, reads committed source
   versions and applies idempotent upserts and deletes in durable
   Scorch batches. Acknowledge only the pending entries whose version
   still matches the durable batch. Commit the batch identifier with
   the index changes and record its acknowledgement in SQLite.
4. **Read one consistent view.** Use a shared publication gate for
   readers, index batch publication and pending acknowledgements. For
   indexed reads, establish the actual SQLite read snapshot inside the
   gate, collect pending IDs and run the bounded Bleve lookup against
   the active generation. The verifier then uses that SQLite snapshot
   after releasing the gate. Source writers can commit new pending
   rows while readers run. Each of those commits is either visible in
   the read snapshot with its pending row or outside that snapshot.
   Use the same gate-before-SQLite order for indexed readers and
   publishers. Keep source transactions independent of the gate.
   Bound the time inside the gate and measure search and writer
   contention. If a batch fails with uncertain index state, disable
   indexed reads before releasing the gate and use the complete SQL path.
   The first experiment uses a short lock and checks the publication
   generation and counter before and after reading candidates. This
   preserves the snapshot rule while concurrent lookups and durable
   batches can overlap. [Implemented protocol](message-search-bleve-experiment.md#index-and-query-behavior)
5. **Recover and retire.** If the process stops after Bleve persists a
   batch and before SQLite acknowledges it, replay pending updates
   idempotently when the persisted batch and source generation can be
   reconciled. Otherwise use SQL while rebuilding. Switch under the same
   gate. Persist the active generation in SQLite once its index is
   durable, and publish the matching live handle before releasing the
   gate. Validate that pair on reopen. Close the old handle after readers
   release it.
   Keep the index out of synchronization and authoritative backups.
   An in-place restore must replay source changes or invalidate the
   generation before indexed reads resume. Reset and deletion must also
   cover the derived files and pending data, with the physical purge
   policy following O10.

Index creation and maintenance belong to the engine lifecycle. Opening
a search can use the ready generation or SQL immediately. The engine
should prioritize message writes and bound the disk, memory and locking
cost of index maintenance. [Engine store](../internal/adapters/sqlite/store.go) · [Scorch persistence](https://github.com/blevesearch/bleve/blob/v2.6.1/index/scorch/scorch.go#L638-L682) · [Index aliases](https://blevesearch.com/docs/IndexAlias/)

## Search highlights and excerpts

The sidebar and global dialog already render literal and typo-tolerant
matches in escaped excerpts and URLs using the Go formatter. The room
timeline currently decorates the selected row and highlights literal
matches in its visible text nodes. A historical result displays its own
excerpt and opens the canonical message in the timeline. [Global formatter](../internal/adapters/web/search_highlight.go) · [Room timeline](../internal/adapters/web/conversation-search.js)

**Recommended first step.** Share the matching-span rules with the
canonical verifier or the existing Oiê formatter. Calculate highlights
only for verified hits and use those spans to choose the excerpt.
Preserve the current presentation with escaped text and application-owned
`<mark>` elements. In the timeline, apply spans only to the selected
visible content and its matching revision. Markdown rendering needs a
mapping from the source spans to visible text nodes. Where that mapping
is unavailable, match the visible text with the same rules or keep the
row highlight. Preserve selection and copy, and keep historical or
metadata-only matches from decorating unrelated text in the current bubble.

**Bleve experiment.** On a separate field with offsets over the exact
original text, `IncludeLocations` can supply term-vector byte ranges
for a verified hit. Native HTML fragments also require the field to be
stored. Evaluate the offsets on the bounded verified page where possible
and let Oiê generate the markup from SQLite text. A Scorch 2.6.1 probe
found `video` for `viedo` in a UTF-8 string containing accents and an emoji. The original byte range was available
without storing the field. The native fragment appeared only with storage
enabled. These ranges can help locate a snippet or reduce repeated
matching work once mapped to the exact field, revision and text hash
verified by SQLite. The prototype's keyword grams and prefix tokens are
candidate hints. Their positions cannot be applied directly to source
text or rendered Markdown. Validate bounds, encoding and provenance
before using any offset, and fall back to Oiê's matcher for unsupported
literal, fuzzy or metadata highlights. Define and version any new span
fields in OpenAPI before sending them to the browser. Measure the size
and latency cost of term vectors before enabling them. [Bleve highlighting](https://blevesearch.com/docs/Highlight%20Matches%20in%20Results/) · [Byte offset contract](https://github.com/blevesearch/bleve/blob/v2.6.1/search/search.go#L62-L71)

## Capabilities by product use

| Capability | Fit for Oiê | Decision status |
| --- | --- | --- |
| Boolean and range filters | Filter by account and room during candidate selection. Later support sender and date in room search, then content type with a definition consistent with the catalog. Use a verified sender identity and the timeline date with explicit local-date boundaries. Validate all filters before pagination in SQLite. | Candidate scoping in R08. New controls and filter semantics remain O08/B32. |
| Phrase and proximity queries | Find an exact phrase in a busy room. A separately documented phrase mode can use ordered tokens from the same verified field and revision. Proximity may help recall fragments when an exact phrase did not occur. | Explore under O08. Plain search preserves the current literal and fuzzy behavior. |
| Facets | Refine large global results by account, conversation or content type. Counts need the same authorization and verified match set as the results. Counts over candidates or an incomplete generation cannot be presented as exact totals. | Explore under O08 after filtering. |
| Relevance and field boosts | Explore a separate relevance order for global search using real search tasks. Compare exact, phrase, fuzzy and metadata matches. A ranked mode needs its own stable pagination semantics. Evaluate BM25 over a field with normal text tokens, since ranking synthetic grams or prefixes changes the meaning of term frequency. | Open O08. The current timeline and default results remain chronological. |
| Generation swapping | Switch to a validated rebuilt index with bounded interruption. Scorch handles index segments and the engine retains responsibility for the SQLite acknowledgement and reader lifetime. | Recommended for index maintenance. |

Keep vector and semantic search outside this increment. Their product
fit depends on separate latency, storage and privacy evaluation. Synonyms
and language-specific stemming can change recall and belong to a relevance
experiment with explicit expected results. [Bleve query types](https://blevesearch.com/docs/Query/) · [Facets](https://blevesearch.com/docs/Result-Faceting/) · [Scoring](https://github.com/blevesearch/bleve/blob/v2.6.1/docs/scoring.md)

## Evidence for activation

The first phase runs behind `OIE_EXPERIMENTAL_BLEVE_SEARCH=1`. See the
[engine experiment](message-search-bleve-experiment.md) for the implemented
publication protocol, activation and recovery behavior, and concurrent-ingest
measurement.

Measure the actual engine endpoint across global and room queries, common
and short selective terms, deep pages and histories with edits and cached
metadata. Compare with the best SQL plan and with the FTS5 benchmark
under the same conditions. Measure cold startup, rebuild and index space,
write and search p95 during archive ingestion, and the effect of term
vectors separately. Compare complete verified pages and cursors in both
plans, including when switching plans between pages. Exercise writes
during rebuild, crashes before and after batch acknowledgement, restore,
reset, metadata changes, alias changes and deletion. Validate highlight
parity across Unicode, transpositions, metadata, plain text and rendered
Markdown. Production activation depends on parity and a repeatable gain
in the target workload. [Benchmark and its limits](message-search-index-benchmark.md)
