//go:build server || !cgo || (!linux && !darwin)

package desktop

import (
	"context"
	"errors"
)

// Run is unavailable in a server-only build, where no native toolkit is linked.
func Run(context.Context, Config) error {
	return errors.New("the desktop host requires a native macOS or Linux build with CGO enabled; use cmd/oie for headless operation")
}
