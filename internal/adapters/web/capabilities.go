package web

import (
	"context"
	"strconv"

	"github.com/araihu/goshtoso/components/accordion"
	"github.com/araihu/goshtoso/components/table"
	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

func (v view) accountDrawerClass() string {
	if v.accountScreen() == "capabilities" {
		return "oie-account-drawer oie-capabilities-drawer"
	}
	if v.accountScreen() == "list" {
		return "oie-account-drawer oie-account-list-drawer"
	}
	if v.accountScreen() == "detail" {
		return "oie-account-drawer oie-account-detail-drawer"
	}
	return "oie-account-drawer"
}
func (v view) capabilitiesHref(id string) string {
	return v.href("open", "accounts", "accountPage", "capabilities", "managedAccount", id, "lang", v.Locale, "theme", v.Theme)
}
func (v view) capabilityBackHref() string {
	return v.href("open", "accounts", "accountPage", "", "managedAccount", v.managedAccountID(), "accountFocus", "capabilities", "lang", v.Locale, "theme", v.Theme)
}
func (s *Server) loadCapabilities(ctx context.Context, v *view) {
	if v.accountScreen() != "capabilities" || v.account(v.managedAccountID()).Address == "" {
		return
	}
	result, err := s.client.GetAccountCapabilitiesWithResponse(ctx, v.managedAccountID())
	if err != nil || result.JSON200 == nil || result.JSON200.AccountId != v.managedAccountID() || result.JSON200.Features == nil {
		return
	}
	v.CapabilityEvidence = result.JSON200
}
func (v view) supportLabel(state engineapi.SupportState, column string) string {
	switch string(state) {
	case "supported":
		return v.Text.CapabilitySupported
	case "partial":
		return v.Text.CapabilityPartial
	case "unsupported":
		if column == "app" {
			return v.Text.CapabilityNotImplemented
		}
		return v.Text.CapabilityUnsupported
	case "not_required":
		return v.Text.CapabilityNotRequired
	case "scope_dependent":
		return v.Text.CapabilityScopeDependent
	case "not_advertised":
		return v.Text.CapabilityNotAdvertised
	default:
		return v.Text.CapabilityUnknown
	}
}
func (v view) scopeLabel(scope string) string {
	switch scope {
	case "local":
		return v.Text.CapabilityScopeLocal
	case "account":
		return v.Text.CapabilityScopeAccount
	case "server":
		return v.Text.CapabilityScopeServer
	case "service":
		return v.Text.CapabilityScopeService
	case "peer":
		return v.Text.CapabilityScopePeer
	case "room":
		return v.Text.CapabilityScopeRoom
	default:
		return v.Text.CapabilityUnknown
	}
}
func (v view) discoveryLabel(state string) string {
	switch state {
	case "not_connected":
		return v.Text.CapabilityNotConnected
	case "pending":
		return v.Text.CapabilityDiscoveryPending
	case "complete":
		return v.Text.CapabilityDiscoveryComplete
	case "partial":
		return v.Text.CapabilityDiscoveryPartial
	case "failed":
		return v.Text.CapabilityDiscoveryFailed
	default:
		return v.Text.CapabilityUnknown
	}
}
func (v view) capabilityLabel(id string) string {
	labels := map[string]string{
		"direct_text": v.Text.CapabilityDirectText, "account_history": v.Text.CapabilityAccountHistory,
		"saved_rooms": v.Text.CapabilitySavedRooms, "room_history": v.Text.CapabilityRoomHistory,
		"room_discovery": v.Text.CapabilityRoomDiscovery, "room_messaging": v.Text.CapabilityRoomMessaging,
		"file_upload": v.Text.CapabilityFileUpload, "image_previews": v.Text.CapabilityImagePreviews,
		"link_previews": v.Text.CapabilityLinkPreviews, "received_replies": v.Text.CapabilityReceivedReplies,
		"send_corrections": v.Text.EditMessage, "send_replies": v.Text.CapabilitySendReplies, "mentions": v.Text.CapabilityMentions, "edit_messages": v.Text.CapabilityEditMessages,
		"retract_messages": v.Text.CapabilityRetractMessages, "reactions": v.Text.CapabilityReactions,
		"read_receipts": v.Text.CapabilityReadReceipts, "typing": v.Text.CapabilityTyping, "presence": v.Text.CapabilityPresence,
		"profile_images": v.Text.CapabilityProfileImages, "formatting": v.Text.CapabilityFormatting, "e2ee": v.Text.CapabilityE2EE,
		"message_search": v.Text.SearchMessages, "message_stars": v.Text.CapabilityStars, "schedules": v.Text.CapabilitySchedules, "sync": v.Text.CapabilitySync,
	}
	if label := labels[id]; label != "" {
		return label
	}
	return id
}
func (v view) capabilityNote(id string) string {
	switch id {
	case "saved_rooms", "room_history":
		return v.Text.CapabilitySavedRoomsNote
	case "presence":
		return v.Text.CapabilityPresenceNote
	case "profile_images":
		return v.Text.CapabilityProfileImagesNote
	case "formatting":
		return v.Text.CapabilityFormattingNote
	case "received_replies":
		return v.Text.CapabilityReceivedRepliesNote
	case "send_corrections":
		return v.Text.CapabilitySendCorrectionsNote
	case "edit_messages":
		return v.Text.CapabilityEditMessagesNote
	case "image_previews", "link_previews", "message_stars":
		return v.Text.CapabilityLocalNote
	default:
		return ""
	}
}
func (v view) capabilityRows() []table.Row {
	rows := []table.Row{}
	if v.CapabilityEvidence == nil {
		return rows
	}
	for _, f := range v.CapabilityEvidence.Features {
		rows = append(rows, table.Row{ID: "capability-" + string(f.Id), Cells: map[string]table.Cell{
			"feature":  {Component: capabilityName(v, f)},
			"app":      {Component: capabilitySupport(v, f.AppSupport, "app")},
			"protocol": {Component: capabilitySupport(v, f.ProtocolSupport, "protocol")},
			"server":   {Component: capabilitySupport(v, f.ServerSupport, "server")},
			"scope":    {Text: v.scopeLabel(string(f.Scope))},
		}})
	}
	return rows
}
func (v view) discoveryItems() []accordion.AccordionItem {
	items := []accordion.AccordionItem{}
	if v.CapabilityEvidence == nil {
		return items
	}
	for i, service := range v.CapabilityEvidence.Discovery.Services {
		items = append(items, accordion.AccordionItem{ID: "discovery-service-" + strconv.Itoa(i), Title: service.Address, Content: discoveryService(v, service)})
	}
	return items
}
