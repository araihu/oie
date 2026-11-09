package xmpp

import (
	"context"
	"encoding/xml"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/araihu/oie/internal/domain"
	mx "mellium.im/xmpp"
	"mellium.im/xmpp/disco"
	"mellium.im/xmpp/jid"
	"mellium.im/xmpp/stanza"
)

const maxDiscoveredServices = 32
const maxServiceQueries = 12

func cleanEvidence(s string, max int) string {
	if !utf8.ValidString(s) || strings.ContainsRune(s, 0) {
		return ""
	}
	chars := []rune(s)
	if len(chars) > max {
		return string(chars[:max])
	}
	return s
}
func discovered(scope, address string, info disco.Info, err error) domain.DiscoveredService {
	result := domain.DiscoveredService{Address: address, Scope: scope, State: "known", Features: []string{}, Identities: []domain.ServiceIdentity{}}
	if err != nil {
		result.State = "failed"
		result.ErrorCode = "discovery_failed"
		return result
	}
	for _, feature := range info.Features {
		if value := cleanEvidence(feature.Var, 1024); value != "" && value == feature.Var {
			result.Features = append(result.Features, value)
		} else {
			result.Truncated = true
		}
	}
	slices.Sort(result.Features)
	result.Features = slices.Compact(result.Features)
	if len(result.Features) > 256 {
		result.Features = result.Features[:256]
		result.Truncated = true
	}
	for i, identity := range info.Identity {
		if i >= 16 {
			result.Truncated = true
			break
		}
		result.Identities = append(result.Identities, domain.ServiceIdentity{Category: cleanEvidence(identity.Category, 300), Type: cleanEvidence(identity.Type, 300), Name: cleanEvidence(identity.Name, 300)})
	}
	return result
}
func cloneDiscovery(d domain.CapabilityDiscovery) domain.CapabilityDiscovery {
	d.Services = slices.Clone(d.Services)
	for i := range d.Services {
		d.Services[i].Features = slices.Clone(d.Services[i].Features)
		d.Services[i].Identities = slices.Clone(d.Services[i].Identities)
	}
	if d.Services == nil {
		d.Services = []domain.DiscoveredService{}
	}
	return d
}
func finishDiscovery(e *entry) {
	known, failed := 0, 0
	for _, service := range e.discovery.Services {
		if service.State == "known" {
			known++
			if service.Truncated {
				failed++
			}
		} else {
			failed++
		}
	}
	e.discovery.State = "complete"
	if known == 0 {
		e.discovery.State = "failed"
	} else if failed > 0 || e.discoveryPartial {
		e.discovery.State = "partial"
	}
}
func recordDiscovery(e *entry, s *mx.Session, service domain.DiscoveredService) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.session != s || e.state.State != "connected" {
		return
	}
	index := -1
	for i, v := range e.discovery.Services {
		if v.Address == service.Address && v.Scope == service.Scope {
			index = i
			break
		}
	}
	if index < 0 {
		if len(e.discovery.Services) >= maxDiscoveredServices {
			e.discoveryPartial = true
			return
		}
		e.discovery.Services = append(e.discovery.Services, service)
	} else {
		e.discovery.Services[index] = service
	}
	e.discovery.CheckedAt = time.Now().UTC()
	if e.discovery.State != "pending" {
		finishDiscovery(e)
	}
}
func (m *Manager) queryInfo(ctx context.Context, e *entry, s *mx.Session, address string) (disco.Info, error) {
	body, err := m.readHistoryIQ(ctx, e, s, address, stanza.GetIQ, `<query xmlns="http://jabber.org/protocol/disco#info"/>`)
	if err != nil {
		return disco.Info{}, err
	}
	var result struct {
		Query *disco.Info `xml:"http://jabber.org/protocol/disco#info query"`
	}
	if err = xml.Unmarshal([]byte("<root>"+body+"</root>"), &result); err != nil {
		return disco.Info{}, err
	}
	if result.Query == nil {
		return disco.Info{}, errArchive
	}
	recordOccupantSupport(e, s, address, *result.Query)
	return *result.Query, nil
}

// A finite session worker discovers only the account, its server and a bounded
// list of server-advertised subdomain services. HTTP reads never start this work.
func (m *Manager) discoverCapabilities(ctx context.Context, e *entry, s *mx.Session) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	account, _ := jid.Parse(e.account.Address)
	server := account.Domain().String()
	for _, target := range []struct{ address, scope string }{{e.account.Address, "account"}, {server, "server"}} {
		info, err := m.queryInfo(ctx, e, s, target.address)
		recordDiscovery(e, s, discovered(target.scope, target.address, info, err))
	}
	payload, err := m.readHistoryIQ(ctx, e, s, server, stanza.GetIQ, `<query xmlns="http://jabber.org/protocol/disco#items"/>`)
	var items struct {
		Query *struct {
			Items []struct {
				Address string `xml:"jid,attr"`
				Node    string `xml:"node,attr"`
			} `xml:"item"`
		} `xml:"http://jabber.org/protocol/disco#items query"`
	}
	if err == nil {
		err = xml.Unmarshal([]byte("<root>"+payload+"</root>"), &items)
		if err == nil && items.Query == nil {
			err = errArchive
		}
	}
	partial := err != nil
	if err == nil {
		seen := map[string]bool{server: true, e.account.Address: true}
		count := 0
		for _, item := range items.Query.Items {
			address, ok := serviceAddress(server, item.Address, item.Node)
			if !ok || seen[address] {
				if !ok && item.Node == "" {
					partial = true
				}
				continue
			}
			seen[address] = true
			if count >= maxServiceQueries {
				partial = true
				break
			}
			count++
			info, queryErr := m.queryInfo(ctx, e, s, address)
			recordDiscovery(e, s, discovered("service", address, info, queryErr))
			if ctx.Err() != nil {
				partial = true
				break
			}
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.session != s || e.state.State != "connected" {
		return
	}
	e.discoveryPartial = e.discoveryPartial || partial
	e.discovery.CheckedAt = time.Now().UTC()
	finishDiscovery(e)
}
func serviceAddress(server, address, node string) (string, bool) {
	if node != "" || len(address) > 1024 {
		return "", false
	}
	j, err := jid.Parse(address)
	if err != nil || j.Localpart() != "" || j.Resourcepart() != "" {
		return "", false
	}
	host := j.Domainpart()
	if host != server && !strings.HasSuffix(host, "."+server) {
		return "", false
	}
	return j.String(), true
}
func (m *Manager) CapabilityEvidence(_ context.Context, a domain.Account) (domain.CapabilityEvidence, error) {
	d := domain.CapabilityDiscovery{State: "not_connected", Services: []domain.DiscoveredService{}}
	m.mu.RLock()
	e := m.entries[a.ID]
	m.mu.RUnlock()
	if e != nil {
		e.mu.RLock()
		if e.state.State == "connected" {
			d = cloneDiscovery(e.discovery)
		}
		e.mu.RUnlock()
	}
	rows := []domain.CapabilityFeature{}
	add := func(id, server string) {
		rows = append(rows, domain.CapabilityFeature{ID: id, ProtocolSupport: "supported", ServerSupport: server})
	}
	for _, id := range []string{"direct_text", "presence", "send_replies"} {
		add(id, "not_required")
	}
	for _, id := range []string{"received_replies", "mentions", "edit_messages", "retract_messages", "reactions", "read_receipts", "typing", "profile_images", "formatting", "e2ee"} {
		add(id, "scope_dependent")
	}
	add("account_history", featureEvidence(d, "account", mamNS))
	add("saved_rooms", bookmarkEvidence(d))
	add("room_history", roomHistoryEvidence(d))
	muc := serviceEvidence(d, "http://jabber.org/protocol/muc")
	add("room_discovery", muc)
	add("room_messaging", muc)
	add("file_upload", serviceEvidence(d, "urn:xmpp:http:upload:0"))
	return domain.CapabilityEvidence{Features: rows, Discovery: d}, nil
}
func hasFeature(service domain.DiscoveredService, feature string) bool {
	return slices.Contains(service.Features, feature)
}
func featureEvidence(d domain.CapabilityDiscovery, scope, feature string) string {
	known := false
	for _, v := range d.Services {
		if v.Scope == scope && v.State == "known" {
			known = known || !v.Truncated
			if hasFeature(v, feature) {
				return "supported"
			}
		}
	}
	if known {
		return "not_advertised"
	}
	return "unknown"
}
func serviceEvidence(d domain.CapabilityDiscovery, feature string) string {
	for _, v := range d.Services {
		if (v.Scope == "server" || v.Scope == "service") && v.State == "known" && hasFeature(v, feature) {
			return "supported"
		}
	}
	if d.State == "complete" {
		return "not_advertised"
	}
	return "unknown"
}
func bookmarkEvidence(d domain.CapabilityDiscovery) string {
	for _, v := range d.Services {
		if (v.Scope == "account" || v.Scope == "server") && v.State == "known" {
			for _, feature := range []string{"jabber:iq:private", "urn:xmpp:bookmarks:1", "urn:xmpp:bookmarks:1#compat", "urn:xmpp:bookmarks:1#compat-pep"} {
				if hasFeature(v, feature) {
					return "supported"
				}
			}
		}
	}
	if d.State == "complete" {
		return "not_advertised"
	}
	return "unknown"
}
func roomHistoryEvidence(d domain.CapabilityDiscovery) string {
	yes, no, failed := 0, 0, 0
	for _, v := range d.Services {
		if v.Scope != "room" {
			continue
		}
		if v.State != "known" || v.Truncated {
			failed++
			continue
		}
		if hasFeature(v, mamNS) {
			yes++
		} else {
			no++
		}
	}
	if yes > 0 {
		if no > 0 || failed > 0 {
			return "partial"
		}
		return "supported"
	}
	if no > 0 && failed == 0 {
		return "not_advertised"
	}
	return "scope_dependent"
}
