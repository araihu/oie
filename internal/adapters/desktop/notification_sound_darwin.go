//go:build darwin && cgo && !server

package desktop

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework AppKit
#import <AppKit/AppKit.h>

// Accessed only on the main thread. The owner keeps the clip for the app's lifetime.
static int oiePlayNotificationSound(const void *bytes, NSUInteger length) {
 @autoreleasepool {
  static NSSound *sound;
  if (!sound) {
   sound = [[NSSound alloc] initWithData:[NSData dataWithBytes:bytes length:length]];
  }
  if ([sound isPlaying]) {
   [sound stop];
  }
  [sound setCurrentTime:0];
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

func (s *runtimeService) PlayNotificationSound(ctx context.Context) error {
	return application.InvokeSyncWithError(func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		wav := web.NotificationSoundWAV()
		if C.oiePlayNotificationSound(unsafe.Pointer(unsafe.SliceData(wav)), C.NSUInteger(len(wav))) == 0 {
			return errors.New("macOS could not play the notification sound")
		}
		return nil
	})
}
