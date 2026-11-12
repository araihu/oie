package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/application"
)

func (s *Server) ResolveDraftLinkPreview(ctx context.Context, r engineapi.ResolveDraftLinkPreviewRequestObject) (engineapi.ResolveDraftLinkPreviewResponseObject, error) {
	if r.Body == nil {
		return nil, application.ErrInvalid
	}
	metadata, err := s.engine.DraftLinkPreview(ctx, r.Body.Url)
	if err != nil {
		return nil, err
	}
	result := engineapi.DraftLinkPreview{Url: r.Body.Url, Image: draftLinkPreviewImage(metadata.Image)}
	if result.Image == nil {
		result.Image = draftLinkPreviewImage(metadata.Favicon)
	}
	if metadata.Title != "" {
		result.Title = &metadata.Title
	}
	if metadata.Description != "" {
		result.Description = &metadata.Description
	}
	if result.Title == nil && result.Description == nil && result.Image == nil {
		return engineapi.ResolveDraftLinkPreview204Response{}, nil
	}
	return engineapi.ResolveDraftLinkPreview200JSONResponse(result), nil
}

func draftLinkPreviewImage(data []byte) *engineapi.DraftLinkPreviewImage {
	if len(data) == 0 || len(data) > 128<<10 {
		return nil
	}
	size, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || size.Width < 1 || size.Height < 1 || size.Width > 2048 || size.Height > 2048 {
		return nil
	}
	switch format {
	case "png", "jpeg", "gif":
		return &engineapi.DraftLinkPreviewImage{
			MediaType: engineapi.DraftLinkPreviewImageMediaType("image/" + format),
			Data:      base64.StdEncoding.EncodeToString(data),
		}
	default:
		return nil
	}
}
