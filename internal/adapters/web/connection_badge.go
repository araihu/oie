package web

import (
	"github.com/araihu/goshtoso/components/avatar"
	"github.com/araihu/goshtoso/components/badge"
)

func connectionAvatarStatus(v view, id string) avatar.Status {
	switch connectionState(v, id) {
	case "connected":
		return avatar.StatusSuccess
	case "connecting":
		return avatar.StatusWarning
	case "error":
		return avatar.StatusDanger
	default:
		return avatar.StatusOffline
	}
}

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
