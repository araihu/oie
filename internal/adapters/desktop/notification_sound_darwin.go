//go:build darwin && cgo && !server

package desktop

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework AppKit
#import <AppKit/AppKit.h>

// Accessed only on the main thread. Keep the active clip alive during playback.
static int oiePlayNotificationSound(const void *bytes, NSUInteger length) {
 @autoreleasepool {
  static NSSound *sound;
  NSSound *next = [[NSSound alloc] initWithData:[NSData dataWithBytes:bytes length:length]];
  if (!next) return 0;
  [sound stop];
  [sound release];
  sound = next;
  return [sound play] ? 1 : 0;
 }
}
*/
import "C"

import (
	"context"
	"errors"
	"unsafe"

	"github.com/araihu/oie/internal/adapters/web"
	"github.com/wailsapp/wails/v3/pkg/application"
)

func (s *runtimeService) PlayNotificationSound(ctx context.Context, wav []byte) error {
	if wav == nil {
		wav = web.NotificationSoundWAV()
	} else if !validNotificationWAV(wav) {
		return errors.New("invalid notification sound")
	}
	return application.InvokeSyncWithError(func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if C.oiePlayNotificationSound(unsafe.Pointer(unsafe.SliceData(wav)), C.NSUInteger(len(wav))) == 0 {
			return errors.New("macOS could not play the notification sound")
		}
		return nil
	})
}
