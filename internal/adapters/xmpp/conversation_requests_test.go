package xmpp

import (
	"testing"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func TestPriorDirectMessageUsesArchiveOrdering(t *testing.T) {
	const peer = "peer@example.test"
	check := domain.DirectHistoryCheck{ArchiveID: "anchor", ProviderID: "first"}
	anchor := application.Incoming{Kind: "direct", Address: peer, ArchiveID: "anchor", ProviderID: "first", OriginalText: "first message"}
	replay := anchor
	replay.ArchiveID = "replay"
	later := application.Incoming{Kind: "direct", Address: peer, ArchiveID: "later", ProviderID: "later", OriginalText: "later message"}
	prior := application.Incoming{Kind: "direct", Address: peer, ArchiveID: "earlier", IsOwn: true, OriginalText: "earlier outgoing message"}
	other := prior
	other.Address = "other@example.test"
	for _, test := range []struct {
		name     string
		messages []application.Incoming
		bounded  bool
		want     bool
	}{
		{"current copy and newer messages", []application.Incoming{replay, anchor, later}, false, false},
		{"wrong peer", []application.Incoming{other, anchor}, false, false},
		{"no anchor", []application.Incoming{prior}, false, false},
		{"bounded before anchor", []application.Incoming{prior}, true, true},
		{"prior on the same page", []application.Incoming{prior, anchor, later}, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := priorDirectMessage(test.messages, peer, check, test.bounded); got != test.want {
				t.Fatalf("prior history = %v, want %v", got, test.want)
			}
		})
	}
}
