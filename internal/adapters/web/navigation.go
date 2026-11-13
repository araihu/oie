package web

import "github.com/a-h/templ"

func sidebarNavigationAttrs() templ.Attributes {
	attrs := applicationNavigationAttrs()
	attrs["data-oie-navigation-item"] = ""
	return attrs
}

// Render navigation behavior before the client runtime's enhancement pass.
func applicationNavigationAttrs() templ.Attributes {
	return navigationAttrs("oie-app", "app", "outerHTML show:none")
}

func navigationAttrs(target, fragment, swap string) templ.Attributes {
	return templ.Attributes{
		"hx-boost":    "true",
		"hx-target":   "#" + target,
		"hx-select":   "#" + target,
		"hx-swap":     swap,
		"hx-push-url": "true",
		"hx-headers":  `{"X-Oie-Fragment":"` + fragment + `"}`,
	}
}
