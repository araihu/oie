//go:build darwin && cgo && !server

package desktop

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Foundation
#import <Foundation/Foundation.h>
#include <stdlib.h>
#include <string.h>
static char *oiePreferredLanguage(void) {
 @autoreleasepool {
  NSString *language = [NSLocale preferredLanguages].firstObject;
  return language ? strdup(language.UTF8String) : NULL;
 }
}
*/
import "C"
import "unsafe"

func platformLanguage() string {
	value := C.oiePreferredLanguage()
	if value == nil {
		return ""
	}
	defer C.free(unsafe.Pointer(value))
	return C.GoString(value)
}
