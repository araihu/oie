package application

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/araihu/oie/internal/domain"
)

// ForwardAttachmentReader obtains a complete, explicitly selected file using
// the same public-address restrictions as other provider media reads.
type ForwardAttachmentReader interface {
	OpenForwardAttachment(context.Context, string) (string, io.ReadCloser, error)
}

func (e *Engine) ForwardMessage(ctx context.Context, key, sourceID, revisionID, destinationID domain.ID) (result MessageResult, err error) {
	if !validID(key) || !validID(sourceID) || !validID(revisionID) || !validID(destinationID) {
		return MessageResult{}, ErrInvalid
	}
	requestFingerprint := fingerprint("forward_message", []domain.ID{sourceID, revisionID, destinationID})
	replay, ok := e.repo.(MessageReplayRepository)
	if !ok {
		return MessageResult{}, ErrProtocolUnavailable
	}
	accepted, err := replay.ReplayAcceptedMessage(ctx, key, requestFingerprint)
	if err != nil {
		return MessageResult{}, err
	}
	if accepted != nil {
		return MessageResult{Message: *accepted, Replayed: true}, nil
	}
	defer func() {
		// A concurrent retry can reach acceptance while this request is staging a file.
		if err != nil && ctx.Err() == nil {
			if accepted, replayErr := replay.ReplayAcceptedMessage(ctx, key, requestFingerprint); replayErr == nil && accepted != nil {
				result, err = MessageResult{Message: *accepted, Replayed: true}, nil
			}
		}
	}()

	source, err := e.Message(ctx, sourceID)
	if err != nil {
		return MessageResult{}, err
	}
	if source.ID != sourceID || source.RevisionID != revisionID || source.SendState == "cancelled" || source.IsWithdrawn() {
		return MessageResult{}, StateConflict{Current: source}
	}
	if _, err = e.messageDestination(ctx, destinationID, ""); err != nil {
		return MessageResult{}, err
	}
	body := domain.DisplayBody(source)
	files := source.Attachments
	var references []domain.SharedReference
	if len(files) == 0 {
		references = forwardFileReferences(body, source.MediaThumbnails)
		body = withoutForwardedLinks(body, references)
		if strings.TrimSpace(body) == "" {
			body = ""
		}
	}
	count := len(files) + len(references)
	if count > MaxMessageAttachments {
		return MessageResult{}, ErrInvalid
	}
	composition := domain.OutgoingComposition{OriginalText: body}
	for index := range count {
		composition.AttachmentIDs = append(composition.AttachmentIDs, forwardAttachmentID(key, requestFingerprint, index))
	}
	if err = validateComposition(composition); err != nil {
		return MessageResult{}, err
	}
	for index, file := range files {
		if err = e.stageForwardedFile(ctx, destinationID, composition.AttachmentIDs[index], source.ConversationID, file, domain.SharedReference{}); err != nil {
			return MessageResult{}, err
		}
	}
	for index, reference := range references {
		if err = e.stageForwardedFile(ctx, destinationID, composition.AttachmentIDs[index], "", domain.StagedAttachment{}, reference); err != nil {
			return MessageResult{}, err
		}
	}
	return e.sendComposition(ctx, key, destinationID, composition, nil, ReplyReview{}, compositionAcceptance{
		fingerprint:   requestFingerprint,
		forwardSource: &ForwardSource{MessageID: sourceID, RevisionID: revisionID},
	})
}

// Retries reuse attachment IDs even when a previous request ended before send.
func forwardAttachmentID(key domain.ID, requestFingerprint string, index int) domain.ID {
	sum := sha256.Sum256([]byte(fmt.Sprintf("forward attachment:%s:%s:%d", key, requestFingerprint, index)))
	variant := sum[2]&0x3f | 0x80
	return domain.ID(fmt.Sprintf("%s-7%03x-%02x%02x-%x", key[:13], uint16(sum[0]&0x0f)<<8|uint16(sum[1]), variant, sum[3], sum[4:10]))
}

func (e *Engine) stageForwardedFile(ctx context.Context, destination, id, sourceConversation domain.ID, file domain.StagedAttachment, reference domain.SharedReference) error {
	repo, err := e.scheduling()
	if err != nil {
		return err
	}
	if _, err = repo.StagedAttachment(ctx, destination, id); !errors.Is(err, ErrNotFound) {
		return err
	}
	var body io.ReadCloser
	if sourceConversation != "" {
		file, body, err = e.OpenStagedAttachment(ctx, sourceConversation, file.ID)
	} else {
		reader, ok := e.mediaReader.(ForwardAttachmentReader)
		if !ok {
			return ErrProtocolUnavailable
		}
		file.FileName = forwardedFileName(reference.FileName)
		file.MediaType, body, err = reader.OpenForwardAttachment(ctx, reference.URL)
	}
	if err != nil {
		return err
	}
	defer func() { _ = body.Close() }()
	_, _, err = e.StageAttachment(ctx, destination, id, file.FileName, file.MediaType, body)
	return err
}

func forwardedFileName(name string) string {
	name = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '/' || r == '\\' {
			return '_'
		}
		return r
	}, name))
	for len(name) > 255 {
		_, width := utf8.DecodeLastRuneInString(name)
		name = name[:len(name)-width]
	}
	if name == "" || name == "." || name == ".." {
		return "attachment"
	}
	return name
}

func forwardFileReferences(body string, thumbnails []domain.MediaThumbnail) []domain.SharedReference {
	var files []domain.SharedReference
	for _, ref := range SharedReferences(body) {
		isFile := ref.Kind != "link"
		for _, thumbnail := range thumbnails {
			isFile = isFile || thumbnail.URL == ref.URL
		}
		if isFile {
			files = append(files, ref)
		}
	}
	return files
}

func withoutForwardedLinks(body string, files []domain.SharedReference) string {
	for _, file := range files {
		address := regexp.QuoteMeta(file.URL)
		markdown := regexp.MustCompile(`!?\[([^\]\n]*)\]\(<?` + address + `>?\)`)
		body = markdown.ReplaceAllString(body, "$1")
		body = strings.ReplaceAll(body, "<"+file.URL+">", "")
		body = messageURLs.ReplaceAllStringFunc(body, func(candidate string) string {
			if strings.TrimRight(candidate, ".,;!)]}") == file.URL {
				return strings.TrimPrefix(candidate, file.URL)
			}
			return candidate
		})
	}
	return body
}
