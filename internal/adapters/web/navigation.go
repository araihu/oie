package web

import "github.com/a-h/templ"

func sidebarNavigationAttrs() templ.Attributes {
	attrs := applicationNavigationAttrs()
	attrs["data-oie-navigation-item"] = ""
	return attrs
}

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
