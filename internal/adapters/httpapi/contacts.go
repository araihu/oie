package httpapi

import (
	"context"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/application"
)

func (s *Server) ListContacts(context.Context, engineapi.ListContactsRequestObject) (engineapi.ListContactsResponseObject, error) {
	return nil, application.ErrProtocolUnavailable
}
