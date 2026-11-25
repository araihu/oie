package httpapi

import (
	"context"
	"io"
	"mime"
	"net/http"
	"strconv"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

type omemoAttachmentResponse struct{ read application.MediaRead }

func (r omemoAttachmentResponse) VisitDownloadOmemoAttachmentResponse(w http.ResponseWriter) error {
	defer func() { _ = r.read.Body.Close() }()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(r.read.Length, 10))
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": r.read.FileName}))
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	control := http.NewResponseController(w)
	_ = control.SetWriteDeadline(time.Now().Add(30 * time.Second))
	defer func() { _ = control.SetWriteDeadline(time.Time{}) }()
	w.WriteHeader(http.StatusOK)
	_, _ = io.CopyN(mediaStreamWriter{writer: w, controller: control}, r.read.Body, r.read.Length)
	return nil
}

func (s *Server) DownloadOmemoAttachment(ctx context.Context, r engineapi.DownloadOmemoAttachmentRequestObject) (engineapi.DownloadOmemoAttachmentResponseObject, error) {
	read, err := s.engine.DownloadOMEMOAttachment(ctx, domain.ID(r.MessageId), r.Index)
	if err != nil {
		return nil, err
	}
	return omemoAttachmentResponse{read: read}, nil
}
