package httpapi

import (
	"context"
	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/application"
)

func (s *Server) ListConversationPreviews(ctx context.Context, r engineapi.ListConversationPreviewsRequestObject) (engineapi.ListConversationPreviewsResponseObject, error) {
	return nil, application.ErrProtocolUnavailable
}
func (s *Server) GetMessagePreview(ctx context.Context, r engineapi.GetMessagePreviewRequestObject) (engineapi.GetMessagePreviewResponseObject, error) {
	return nil, application.ErrProtocolUnavailable
}
func (s *Server) DownloadPreviewImage(ctx context.Context, r engineapi.DownloadPreviewImageRequestObject) (engineapi.DownloadPreviewImageResponseObject, error) {
	return nil, application.ErrProtocolUnavailable
}
func (s *Server) GetMediaImage(ctx context.Context, r engineapi.GetMediaImageRequestObject) (engineapi.GetMediaImageResponseObject, error) {
	return nil, application.ErrProtocolUnavailable
}
