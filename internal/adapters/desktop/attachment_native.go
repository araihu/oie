//go:build (linux || darwin) && cgo && !server

package desktop

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/google/uuid"

	wails "github.com/wailsapp/wails/v3/pkg/application"
)

type attachmentSelection struct {
	Files []engineapi.StagedAttachment `json:"files"`
	Limit bool                         `json:"limit"`
	Error bool                         `json:"error"`
}

// SelectMedia grants the WebView local access to this portal selection.
func (s *runtimeService) SelectMedia(ctx context.Context, remaining int) (portalSelection, error) {
	if desktopAppID() != flatpakAppID || remaining < 1 || remaining > 16 {
		return portalSelection{}, errors.New("invalid media selection")
	}
	paths, err := s.pickAttachmentPaths(ctx, false)
	if err != nil {
		return portalSelection{}, err
	}
	return s.media.open(paths, remaining)
}

func (s *runtimeService) ReadMediaChunk(id string, index int, offset int64) ([]byte, error) {
	return s.media.read(id, index, offset)
}

func (s *runtimeService) ReleaseMedia(id string) {
	s.media.release(id)
}

func (s *runtimeService) pickAttachmentPaths(ctx context.Context, imagesOnly bool) ([]string, error) {
	s.dialogMu.Lock()
	defer s.dialogMu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	dialog := s.app.Dialog.OpenFile()
	options := wails.OpenFileDialogOptions{
		Title: s.labels.AttachFiles, Window: s.app.Window.Current(),
		CanChooseFiles: true, AllowsMultipleSelection: true, AllowsOtherFileTypes: !imagesOnly,
	}
	if imagesOnly {
		options.Title = s.labels.AttachImages
		options.Filters = []wails.FileFilter{{
			DisplayName: s.labels.AttachImages,
			Pattern:     "*.png;*.jpg;*.jpeg;*.webp;*.gif;*.avif;*.svg;*.bmp;*.ico;*.tiff;*.tif;*.heic;*.heif",
		}}
	}
	dialog.SetOptions(&options)
	return dialog.PromptForMultipleSelection()
}

// AttachFiles stages the files selected in the Flatpak's native chooser. The
// nested WebKit sandbox cannot read the paths provided by the Documents portal.
func (s *runtimeService) AttachFiles(ctx context.Context, engineID, conversationID, csrf string, remaining int, imagesOnly bool) (attachmentSelection, error) {
	server := s.server.Load()
	if desktopAppID() != flatpakAppID || server == nil || !validUUID(engineID) || !validUUID(conversationID) || remaining < 1 || remaining > 16 {
		return attachmentSelection{}, errors.New("invalid desktop attachment request")
	}
	paths, err := s.pickAttachmentPaths(ctx, imagesOnly)
	if err != nil {
		return attachmentSelection{}, err
	}
	if len(paths) > remaining {
		return attachmentSelection{Limit: true}, nil
	}
	// Validate the selection before staging its first file.
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			return attachmentSelection{Error: true}, nil
		}
	}

	result := attachmentSelection{Files: make([]engineapi.StagedAttachment, 0, len(paths))}
	handler := s.cookies.handler(server)
	for _, path := range paths {
		file, err := stageNativeAttachment(ctx, handler, path, engineID, conversationID, csrf)
		if err != nil {
			result.Error = true
			break
		}
		result.Files = append(result.Files, file)
	}
	return result, nil
}

func stageNativeAttachment(ctx context.Context, handler http.Handler, path, engineID, conversationID, csrf string) (engineapi.StagedAttachment, error) {
	file, err := os.Open(path)
	if err != nil {
		return engineapi.StagedAttachment{}, err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return engineapi.StagedAttachment{}, errors.New("attachment must be a regular file")
	}

	id, err := uuid.NewV7()
	if err != nil {
		return engineapi.StagedAttachment{}, err
	}
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	name := filepath.Base(path)
	mediaType := mime.TypeByExtension(strings.ToLower(filepath.Ext(name)))
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{"name": "file", "filename": name}))
	header.Set("Content-Type", mediaType)
	_, err = form.CreatePart(header)
	if err != nil {
		return engineapi.StagedAttachment{}, err
	}
	// Stream the selected file between the multipart header and closing boundary.
	headerSize := body.Len()
	if err := form.Close(); err != nil {
		return engineapi.StagedAttachment{}, err
	}
	query := url.Values{"engineId": {engineID}, "conversationId": {conversationID}, "attachmentId": {id.String()}}
	payload := io.MultiReader(bytes.NewReader(body.Bytes()[:headerSize]), file, bytes.NewReader(body.Bytes()[headerSize:]))
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "wails://localhost/ui/scheduling/stage?"+query.Encode(), payload)
	if err != nil {
		return engineapi.StagedAttachment{}, err
	}
	request.Header.Set("Content-Type", form.FormDataContentType())
	request.Header.Set("X-CSRF-Token", csrf)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		return engineapi.StagedAttachment{}, fmt.Errorf("attachment staging failed with HTTP status %d", response.Code)
	}
	var staged engineapi.StagedAttachment
	if err := json.Unmarshal(response.Body.Bytes(), &staged); err != nil {
		return engineapi.StagedAttachment{}, err
	}
	if staged.Id != id.String() || staged.FileName == "" || staged.Size != info.Size() {
		return engineapi.StagedAttachment{}, errors.New("attachment metadata does not match the selected file")
	}
	return staged, nil
}
