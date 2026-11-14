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
	"github.com/araihu/oie/internal/application"
	"github.com/google/uuid"

	wails "github.com/wailsapp/wails/v3/pkg/application"
)

type attachmentSelection struct {
	Files []engineapi.StagedAttachment `json:"files"`
	Limit bool                         `json:"limit"`
	Error bool                         `json:"error"`
}

// AttachFiles stages the files selected in the Flatpak's native chooser. The
// nested WebKit sandbox cannot read the paths provided by the Documents portal.
func (s *runtimeService) AttachFiles(ctx context.Context, engineID, conversationID, csrf string, remaining int, imagesOnly bool) (attachmentSelection, error) {
	server := s.server.Load()
	if desktopAppID() != flatpakAppID || server == nil || !validUUID(engineID) || !validUUID(conversationID) || remaining < 1 || remaining > 16 {
		return attachmentSelection{}, errors.New("invalid desktop attachment request")
	}
	s.dialogMu.Lock()
	defer s.dialogMu.Unlock()
	if err := ctx.Err(); err != nil {
		return attachmentSelection{}, err
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
	paths, err := dialog.PromptForMultipleSelection()
	if err != nil {
		return attachmentSelection{}, err
	}
	if len(paths) > remaining {
		return attachmentSelection{Limit: true}, nil
	}
	// Enforce the limit before staging any file from this selection.
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			return attachmentSelection{Error: true}, nil
		}
		if info.Size() > application.MaxStagedAttachmentSize {
			return attachmentSelection{Limit: true}, nil
		}
	}

	result := attachmentSelection{Files: make([]engineapi.StagedAttachment, 0, len(paths))}
	handler := s.cookies.handler(server.Handler())
	for _, path := range paths {
		file, err := stageNativeAttachment(ctx, handler, path, engineID, conversationID, csrf)
		if err != nil {
			result.Limit = errors.Is(err, application.ErrAttachmentTooLarge)
			result.Error = !result.Limit
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
	part, err := form.CreatePart(header)
	if err != nil {
		return engineapi.StagedAttachment{}, err
	}
	size, err := io.Copy(part, io.LimitReader(file, application.MaxStagedAttachmentSize+1))
	if err != nil {
		return engineapi.StagedAttachment{}, err
	}
	if size > application.MaxStagedAttachmentSize {
		return engineapi.StagedAttachment{}, application.ErrAttachmentTooLarge
	}
	if err := form.Close(); err != nil {
		return engineapi.StagedAttachment{}, err
	}
	query := url.Values{"engineId": {engineID}, "conversationId": {conversationID}, "attachmentId": {id.String()}}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "wails://localhost/ui/scheduling/stage?"+query.Encode(), &body)
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
	if staged.Id != id.String() || staged.FileName == "" || staged.Size != size {
		return engineapi.StagedAttachment{}, errors.New("attachment metadata does not match the selected file")
	}
	return staged, nil
}
