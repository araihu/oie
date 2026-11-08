package application

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/araihu/oie/internal/domain"
)

type scopedMediaRepository struct {
	Repository
	MessagingRepository
	item                   domain.SharedContentItem
	source                 domain.Message
	itemError, sourceError error
}

func (r scopedMediaRepository) SharedContentItem(context.Context, domain.ID) (domain.SharedContentItem, error) {
	return r.item, r.itemError
}
func (r scopedMediaRepository) Message(context.Context, domain.ID) (domain.Message, error) {
	return r.source, r.sourceError
}

type countedMediaReader struct {
	calls   int
	closed  bool
	invalid bool
}
type countedMediaBody struct {
	io.Reader
	closed *bool
}

func (b countedMediaBody) Close() error { *b.closed = true; return nil }
func (r *countedMediaReader) Open(context.Context, string, MediaReadRequest) (MediaRead, error) {
	r.calls++
	mime := "video/mp4"
	if r.invalid {
		mime = "text/html"
	}
	return MediaRead{Body: countedMediaBody{Reader: strings.NewReader("video"), closed: &r.closed}, MediaType: mime, Total: 5, Length: 5}, nil
}
func TestSharedMediaChecksCurrentCanonicalSourceBeforeNetwork(t *testing.T) {
	message := domain.Message{ID: "019a5df0-0000-7000-8000-000000000001", AccountID: "019a5df0-0000-7000-8000-000000000002", ConversationID: "019a5df0-0000-7000-8000-000000000003", OriginalText: "https://files.example/video.mp4"}
	item := domain.SharedContentItem{SharedReference: domain.SharedReference{ID: "019a5df0-0000-7000-8000-000000000004", Kind: "video", URL: message.OriginalText, FileName: "video.mp4"}, Message: message}
	for _, kind := range []string{"current", "missing", "source-deleted", "corrected-url", "canonical-alias", "foreign-account", "foreign-conversation", "wrong-item", "non-video"} {
		t.Run(kind, func(t *testing.T) {
			repo := scopedMediaRepository{item: item, source: message}
			switch kind {
			case "missing":
				repo.itemError = ErrNotFound
			case "source-deleted":
				repo.sourceError = ErrNotFound
			case "corrected-url":
				repo.source.OriginalText = "message no longer links video"
			case "canonical-alias":
				repo.source.ID = "019a5df0-0000-7000-8000-000000000005"
			case "foreign-account":
				repo.source.AccountID = "019a5df0-0000-7000-8000-000000000005"
			case "foreign-conversation":
				repo.source.ConversationID = "019a5df0-0000-7000-8000-000000000005"
			case "wrong-item":
				repo.item.ID = "019a5df0-0000-7000-8000-000000000005"
			case "non-video":
				repo.item.Kind = "image"
			}
			e, err := New(repo, "local", nil, time.Now)
			if err != nil {
				t.Fatal(err)
			}
			reader := &countedMediaReader{}
			e.SetMediaReader(reader)
			result, err := e.SharedContentMedia(t.Context(), item.ID, MediaReadRequest{})
			if kind == "current" {
				if err != nil || reader.calls != 1 || result.FileName != "video.mp4" {
					t.Fatal(result, err)
				}
				_ = result.Body.Close()
				return
			}
			if !errors.Is(err, ErrNotFound) || reader.calls != 0 {
				t.Fatal("unavailable source contacted provider", kind, err, reader.calls)
			}
		})
	}
	repo := scopedMediaRepository{item: item, source: message}
	e, _ := New(repo, "local", nil, time.Now)
	reader := &countedMediaReader{invalid: true}
	e.SetMediaReader(reader)
	if _, err := e.SharedContentMedia(t.Context(), item.ID, MediaReadRequest{}); err == nil || !reader.closed {
		t.Fatal("invalid provider port result not closed", err)
	}
}
func TestMediaByteRangesResolveWithoutOverflow(t *testing.T) {
	for _, tt := range []struct {
		rangeValue        ByteRange
		total, start, end int64
		valid             bool
	}{
		{ByteRange{Start: 0, End: -1}, 10, 0, 9, true}, {ByteRange{Start: 3, End: 999}, 10, 3, 9, true}, {ByteRange{Start: 99, End: -1, Suffix: true}, 10, 0, 9, true}, {ByteRange{Start: 3, End: -1, Suffix: true}, 10, 7, 9, true}, {ByteRange{Start: 10, End: -1}, 10, 0, 0, false}, {ByteRange{Start: 0, End: -1, Suffix: true}, 10, 0, 0, false}, {ByteRange{Start: 0, End: -1}, 0, 0, 0, false}, {ByteRange{Start: 4, End: 2}, 10, 0, 0, false},
	} {
		start, end, valid := tt.rangeValue.Resolve(tt.total)
		if valid != tt.valid || valid && (start != tt.start || end != tt.end) {
			t.Fatal(tt, start, end, valid)
		}
	}
}

type documentMediaReader struct {
	calls  int
	format MediaFormat
	mime   string
	total  int64
	closed bool
}

func (r *documentMediaReader) Open(_ context.Context, _ string, request MediaReadRequest) (MediaRead, error) {
	r.calls++
	r.format = request.Format
	return MediaRead{Body: countedMediaBody{Reader: strings.NewReader("data"), closed: &r.closed}, MediaType: r.mime, Length: r.total, Total: r.total}, nil
}
func TestSharedDocumentCanonicalClassificationAndNoUnsupportedFetch(t *testing.T) {
	for _, extension := range []string{"txt", "log", "md", "csv", "json", "xml", "pdf", "html", "svg", "docx", "zip"} {
		t.Run(extension, func(t *testing.T) {
			message := domain.Message{ID: "019a5df0-0000-7000-8000-000000000001", AccountID: "019a5df0-0000-7000-8000-000000000002", ConversationID: "019a5df0-0000-7000-8000-000000000003", OriginalText: "https://files.example/report." + extension}
			refs := SharedReferences(message.OriginalText)
			if len(refs) != 1 {
				t.Fatal(refs)
			}
			item := domain.SharedContentItem{SharedReference: refs[0], Message: message}
			item.ID = "019a5df0-0000-7000-8000-000000000004"
			e, _ := New(scopedMediaRepository{item: item, source: message}, "local", nil, time.Now)
			reader := &documentMediaReader{mime: "text/plain", total: 4}
			if extension == "pdf" {
				reader.mime = "application/pdf"
			}
			e.SetMediaReader(reader)
			result, err := e.SharedContentMedia(t.Context(), item.ID, MediaReadRequest{Format: "video"})
			if MediaFormat(extension).Limit() == 0 {
				if !errors.Is(err, ErrNotFound) || reader.calls != 0 {
					t.Fatal("unsupported document fetched", err, reader.calls)
				}
				return
			}
			if err != nil || reader.calls != 1 || reader.format != MediaFormat(extension) || result.FileName != "report."+extension {
				t.Fatal(result, err, reader)
			}
			_ = result.Body.Close()
		})
	}
}
func TestSharedDocumentRejectsInvalidReaderAndStaleSource(t *testing.T) {
	message := domain.Message{ID: "019a5df0-0000-7000-8000-000000000001", AccountID: "019a5df0-0000-7000-8000-000000000002", ConversationID: "019a5df0-0000-7000-8000-000000000003", OriginalText: "https://files.example/report.pdf"}
	item := domain.SharedContentItem{SharedReference: domain.SharedReference{ID: "019a5df0-0000-7000-8000-000000000004", Kind: "document", URL: message.OriginalText, FileName: "report.pdf"}, Message: message}
	for _, tt := range []struct {
		name, mime string
		total      int64
	}{{"mismatch", "text/plain", 4}, {"oversize", "application/pdf", MaxPDFSource + 1}} {
		t.Run(tt.name, func(t *testing.T) {
			e, _ := New(scopedMediaRepository{item: item, source: message}, "local", nil, time.Now)
			reader := &documentMediaReader{mime: tt.mime, total: tt.total}
			e.SetMediaReader(reader)
			if _, err := e.SharedContentMedia(t.Context(), item.ID, MediaReadRequest{}); err == nil || !reader.closed {
				t.Fatal("invalid reader result accepted or leaked", err)
			}
		})
	}
	message.OriginalText = "corrected without document"
	e, _ := New(scopedMediaRepository{item: item, source: message}, "local", nil, time.Now)
	reader := &documentMediaReader{}
	e.SetMediaReader(reader)
	if _, err := e.SharedContentMedia(t.Context(), item.ID, MediaReadRequest{}); !errors.Is(err, ErrNotFound) || reader.calls != 0 {
		t.Fatal("stale source fetched", err)
	}
}
