package httpapi

import (
	"context"
	"net/http"
	"strings"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/domain"
)

func avatar(a domain.Avatar) engineapi.Avatar {
	return engineapi.Avatar{Id: string(a.ID), RevisionId: string(a.RevisionID), AccountId: string(a.AccountID), Address: a.Address, ImageHash: a.ImageHash, UpdatedAt: a.UpdatedAt}
}
func (s *Server) ListAccountAvatars(ctx context.Context, r engineapi.ListAccountAvatarsRequestObject) (engineapi.ListAccountAvatarsResponseObject, error) {
	limit := 100
	if r.Params.Limit != nil {
		limit = *r.Params.Limit
	}
	after := domain.ID("")
	if r.Params.After != nil {
		after = domain.ID(*r.Params.After)
	}
	rows, err := s.engine.Avatars(ctx, domain.ID(r.AccountId), after, limit)
	if err != nil {
		return nil, err
	}
	page := engineapi.AvatarPage{Items: []engineapi.Avatar{}}
	if len(rows) > limit {
		next := string(rows[limit-1].ID)
		page.NextCursor = &next
		rows = rows[:limit]
	}
	for _, a := range rows {
		page.Items = append(page.Items, avatar(a))
	}
	return engineapi.ListAccountAvatars200JSONResponse(page), nil
}

type avatarImageResponse struct {
	data      []byte
	etag      string
	unchanged bool
}

func (r avatarImageResponse) VisitGetAvatarImageResponse(w http.ResponseWriter) error {
	w.Header().Set("ETag", r.etag)
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.unchanged {
		w.WriteHeader(http.StatusNotModified)
		return nil
	}
	w.Header().Set("Content-Type", "image/png")
	_, err := w.Write(r.data)
	return err
}
func (s *Server) GetAvatarImage(ctx context.Context, r engineapi.GetAvatarImageRequestObject) (engineapi.GetAvatarImageResponseObject, error) {
	data, err := s.engine.AvatarImage(ctx, r.Hash)
	if err != nil {
		return nil, err
	}
	result := avatarImageResponse{data: data, etag: `"` + r.Hash + `"`}
	if r.Params.IfNoneMatch != nil {
		for _, tag := range strings.Split(*r.Params.IfNoneMatch, ",") {
			value := strings.TrimPrefix(strings.TrimSpace(tag), "W/")
			if value == result.etag || value == "*" {
				result.unchanged = true
				break
			}
		}
	}
	return result, nil
}
