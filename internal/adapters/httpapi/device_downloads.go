package httpapi

import (
	"context"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func (s *Server) GetDownloadRetention(ctx context.Context, r engineapi.GetDownloadRetentionRequestObject) (engineapi.GetDownloadRetentionResponseObject, error) {
	policy, err := s.engine.DownloadPolicy(ctx, domain.ID(r.DeviceId))
	return engineapi.GetDownloadRetention200JSONResponse{Days: policy.Days, Count: policy.Count}, err
}

func (s *Server) SetDownloadRetention(ctx context.Context, r engineapi.SetDownloadRetentionRequestObject) (engineapi.SetDownloadRetentionResponseObject, error) {
	if r.Body == nil {
		return nil, application.ErrInvalid
	}
	policy, err := s.engine.SetDownloadPolicy(ctx, domain.ID(r.DeviceId), r.Body.Days, false)
	return engineapi.SetDownloadRetention200JSONResponse{Days: policy.Days, Count: policy.Count}, err
}

func (s *Server) ClearDeviceDownloads(ctx context.Context, r engineapi.ClearDeviceDownloadsRequestObject) (engineapi.ClearDeviceDownloadsResponseObject, error) {
	policy, err := s.engine.SetDownloadPolicy(ctx, domain.ID(r.DeviceId), 0, true)
	return engineapi.ClearDeviceDownloads200JSONResponse{Days: policy.Days, Count: policy.Count}, err
}
