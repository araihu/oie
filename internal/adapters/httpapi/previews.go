package httpapi

import (
	"context"
	"net/http"
	"strings"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func preview(p domain.MessagePreview) engineapi.MessagePreview {
	out := engineapi.MessagePreview{Id: string(p.ID), RevisionId: string(p.RevisionID), AccountId: string(p.AccountID), ConversationId: string(p.ConversationID), MessageId: string(p.MessageID), Url: p.URL, Kind: engineapi.MessagePreviewKind(p.Kind), State: engineapi.MessagePreviewState(p.State), DownloadState: engineapi.MessagePreviewDownloadState(p.DownloadState), UpdatedAt: p.UpdatedAt}
	ptr := func(s string) *string {
		if s == "" {
			return nil
		}
		return &s
	}
	out.Title = ptr(p.Title)
	out.Description = ptr(p.Description)
	out.SiteName = ptr(p.SiteName)
	out.ImageHash = ptr(p.ImageHash)
	out.FaviconHash = ptr(p.FaviconHash)
	out.FileHash = ptr(p.FileHash)
	out.LastErrorCode = ptr(p.LastErrorCode)
	if p.FileHash != "" {
		mime := engineapi.MessagePreviewFileMimeType(p.FileMimeType)
		integrity := engineapi.ComputedOnly
		out.FileMimeType = &mime
		out.FileSize = &p.FileSize
		out.Integrity = &integrity
	}
	return out
}
func (s *Server) ListConversationPreviews(ctx context.Context, r engineapi.ListConversationPreviewsRequestObject) (engineapi.ListConversationPreviewsResponseObject, error) {
	var ids []domain.ID
	if r.Params.MessageId != nil {
		for _, id := range *r.Params.MessageId {
			ids = append(ids, domain.ID(id))
		}
	}
	rows, err := s.engine.ConversationPreviews(ctx, domain.ID(r.ConversationId), ids)
	if err != nil {
		return nil, err
	}
	page := engineapi.PreviewPage{Items: []engineapi.MessagePreview{}}
	for _, p := range rows {
		page.Items = append(page.Items, preview(p))
	}
	return engineapi.ListConversationPreviews200JSONResponse(page), nil
}
func (s *Server) GetMessagePreview(ctx context.Context, r engineapi.GetMessagePreviewRequestObject) (engineapi.GetMessagePreviewResponseObject, error) {
	p, err := s.engine.Preview(ctx, domain.ID(r.PreviewId))
	if err == nil && p.Kind == "video" {
		return nil, application.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return engineapi.GetMessagePreview200JSONResponse(preview(p)), nil
}
func (s *Server) DownloadPreviewImage(ctx context.Context, r engineapi.DownloadPreviewImageRequestObject) (engineapi.DownloadPreviewImageResponseObject, error) {
	p, err := s.engine.DownloadPreview(ctx, domain.ID(r.PreviewId))
	if err != nil {
		return nil, err
	}
	if p.DownloadState == "ready" {
		return engineapi.DownloadPreviewImage200JSONResponse(preview(p)), nil
	}
	return engineapi.DownloadPreviewImage202JSONResponse(preview(p)), nil
}

type mediaImageResponse struct {
	data      []byte
	file      domain.MediaFile
	unchanged bool
}

func (r mediaImageResponse) VisitGetMediaImageResponse(w http.ResponseWriter) error {
	w.Header().Set("ETag", `"`+r.file.Hash+`"`)
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	if r.unchanged {
		w.WriteHeader(http.StatusNotModified)
		return nil
	}
	w.Header().Set("Content-Type", r.file.MimeType)
	w.Header().Set("Content-Disposition", "inline")
	_, err := w.Write(r.data)
	return err
}
func (s *Server) GetMediaImage(ctx context.Context, r engineapi.GetMediaImageRequestObject) (engineapi.GetMediaImageResponseObject, error) {
	data, file, err := s.engine.MediaImage(ctx, r.Hash)
	if err != nil {
		return nil, err
	}
	result := mediaImageResponse{data: data, file: file}
	if r.Params.IfNoneMatch != nil {
		for _, tag := range strings.Split(*r.Params.IfNoneMatch, ",") {
			value := strings.TrimPrefix(strings.TrimSpace(tag), "W/")
			if value == `"`+file.Hash+`"` || value == "*" {
				result.unchanged = true
				break
			}
		}
	}
	return result, nil
}

func (s *Server) RefreshMessagePreview(ctx context.Context, r engineapi.RefreshMessagePreviewRequestObject) (engineapi.RefreshMessagePreviewResponseObject, error) {
	p, err := s.engine.RefreshPreview(ctx, domain.ID(r.PreviewId))
	if err != nil {
		return nil, err
	}
	return engineapi.RefreshMessagePreview202JSONResponse(preview(p)), nil
}

func (s *Server) GetVideoThumbnail(ctx context.Context, r engineapi.GetVideoThumbnailRequestObject) (engineapi.GetVideoThumbnailResponseObject, error) {
	p, err := s.engine.VideoThumbnail(ctx, domain.ID(r.ItemId))
	if err != nil {
		return nil, err
	}
	return engineapi.GetVideoThumbnail200JSONResponse(videoThumbnail(p)), nil
}
func (s *Server) RequestVideoThumbnail(ctx context.Context, r engineapi.RequestVideoThumbnailRequestObject) (engineapi.RequestVideoThumbnailResponseObject, error) {
	p, err := s.engine.RequestVideoThumbnail(ctx, domain.ID(r.ItemId))
	if err != nil {
		return nil, err
	}
	if p.State == "ready" {
		return engineapi.RequestVideoThumbnail200JSONResponse(videoThumbnail(p)), nil
	}
	return engineapi.RequestVideoThumbnail202JSONResponse(videoThumbnail(p)), nil
}

func videoThumbnail(p domain.MessagePreview) engineapi.VideoThumbnail {
	out := engineapi.VideoThumbnail{Id: string(p.ID), RevisionId: string(p.RevisionID), SharedContentId: string(p.SharedContentID), AccountId: string(p.AccountID), ConversationId: string(p.ConversationID), MessageId: string(p.MessageID), Url: p.URL, Kind: "video", State: engineapi.VideoThumbnailState(p.State), UpdatedAt: p.UpdatedAt}
	if p.ImageHash != "" {
		out.ImageHash = &p.ImageHash
	}
	if p.LastErrorCode != "" {
		out.LastErrorCode = &p.LastErrorCode
	}
	return out
}
