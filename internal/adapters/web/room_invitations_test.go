package web

import (
	"bytes"
	"strings"
	"testing"
)

func TestRoomInvitationLinks(t *testing.T) {
	const address = "socializando-saberes@salas.suchat.org"
	for _, tc := range []struct {
		source string
		links  int
	}{
		{"Entre em xmpp:" + address + "?join.", 1},
		{"**XMPP:" + address + "?join**", 1},
		{"<xmpp:" + address + "?join>", 1},
		{"[Sala](xmpp:" + address + "?join)", 1},
		{"`xmpp:" + address + "?join`", 0},
		{"```\nxmpp:" + address + "?join\n```", 0},
		{"xmpp:" + address + "?message", 0},
		{"xmpp:" + address + "?join;password=example", 0},
		{"xmpp://alice@example.org/" + address + "?join", 0},
		{"xmpp:%3Cscript%3E@example.org?join", 0},
	} {
		t.Run(tc.source, func(t *testing.T) {
			var out bytes.Buffer
			if err := formattedMessage(t.Context(), tc.source).Render(t.Context(), &out); err != nil {
				t.Fatal(err)
			}
			want := `data-room-invitation="` + address + `"`
			if got := strings.Count(out.String(), want); got != tc.links || strings.Count(out.String(), "data-room-invitation=") != tc.links {
				t.Fatalf("want %d invitation links, got %s", tc.links, out.String())
			}
		})
	}
}
