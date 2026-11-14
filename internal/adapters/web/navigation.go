package web

import "github.com/a-h/templ"

func sidebarNavigationAttrs() templ.Attributes {
	attrs := navigationAttrs("oie-route", "app", "outerHTML settle:0ms show:none")
	attrs["data-oie-navigation-item"] = ""
	return attrs
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
