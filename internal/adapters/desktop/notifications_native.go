//go:build (linux || darwin) && cgo && !server

package desktop

import (
	"context"
	"errors"
	"html"
	"runtime"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/araihu/oie/internal/domain"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"
)

type desktopNotifications struct {
	mu      sync.Mutex
	service *notifications.NotificationService
	closed  bool
	open    func([]string)
}

// Start the platform service on demand. A missing notification daemon or
// unbundled macOS binary must not prevent the application from opening.
func (n *desktopNotifications) start(ctx context.Context) (*notifications.NotificationService, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.closed {
		return nil, errors.New("desktop notifications closed")
	}
	if n.service != nil {
		return n.service, nil
	}
	service := notifications.New()
	if err := service.ServiceStartup(ctx, application.ServiceOptions{}); err != nil {
		return nil, err
	}
	service.OnNotificationResponse(func(result notifications.NotificationResult) {
		if result.Error != nil || n.open == nil {
			return
		}
		action := result.Response.ActionIdentifier
		if action != notifications.DefaultActionIdentifier {
			return
		}
		ids := strings.Split(result.Response.ID, ":")
		if len(ids) != 4 || !validNotificationIDs(ids...) {
			return
		}
		n.open(ids)
	})
	n.service = service
	return service, nil
}

func (n *desktopNotifications) close() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.closed = true
	if n.service != nil {
		n.service.OnNotificationResponse(nil)
		_ = n.service.ServiceShutdown()
	}
}

func validNotificationIDs(values ...string) bool {
	for _, value := range values {
		if _, err := domain.ParseID(value); err != nil {
			return false
		}
	}
	return true
}

func (s *runtimeService) RequestNotifications(ctx context.Context) (bool, error) {
	service, err := s.alerts.start(ctx)
	if err != nil {
		return false, err
	}
	return service.RequestNotificationAuthorization()
}

func (s *runtimeService) NotifyMessage(ctx context.Context, engine, account, conversation, message, title, body string) error {
	ids := []string{engine, account, conversation, message}
	if !validNotificationIDs(ids...) || title == "" || utf8.RuneCountInString(title) > 120 || utf8.RuneCountInString(body) > 120 {
		return errors.New("invalid message notification")
	}
	service, err := s.alerts.start(ctx)
	if err != nil {
		return err
	}
	if runtime.GOOS == "linux" {
		body = html.EscapeString(body)
	}
	return service.SendNotification(notifications.NotificationOptions{
		ID: strings.Join(ids, ":"), Title: title, Body: body,
		Sound: &notifications.NotificationSound{Silent: true},
	})
}
