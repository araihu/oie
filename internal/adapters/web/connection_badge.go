package web

import "github.com/araihu/goshtoso/components/badge"

func connectionTone(v view, id string) badge.Tone {
	switch connectionState(v, id) {
	case "connected":
		return badge.ToneSuccess
	case "connecting":
		return badge.ToneWarning
	case "error":
		return badge.ToneDanger
	default:
		return badge.ToneDefault
	}
}
