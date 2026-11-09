package web

import "github.com/a-h/templ"

// Render navigation behavior before the client runtime's enhancement pass.
func applicationNavigationAttrs() templ.Attributes {
	return templ.Attributes{
		"hx-boost":    "true",
		"hx-target":   "#oie-app",
		"hx-select":   "#oie-app",
		"hx-swap":     "outerHTML show:none",
		"hx-push-url": "true",
		"hx-headers":  `{"X-Oie-Fragment":"app"}`,
	}
}
