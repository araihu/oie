package web

import (
	"bytes"
	"context"
	"html"
	"io"
	"net/url"
	"regexp"
	"strings"
	"unicode"

	"github.com/a-h/templ"
	htmlstream "golang.org/x/net/html"
)

var roomInvitationPattern = regexp.MustCompile("(?i)\\bxmpp:[^\\s<>\"'`()\\[\\]{}]+")

func bareRoomInput(value string) bool {
	if len(value) < 3 || len(value) > 2047 || strings.ContainsAny(value, "/?#:<>&'\"") || strings.IndexFunc(value, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) >= 0 {
		return false
	}
	local, host, ok := strings.Cut(value, "@")
	return ok && local != "" && host != "" && !strings.ContainsRune(host, '@')
}

func invitationRoom(value string) string {
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "xmpp" || u.Opaque == "" || u.RawQuery != "join" || strings.ContainsRune(value, '#') {
		return ""
	}
	room, err := url.PathUnescape(u.Opaque)
	if err != nil || !bareRoomInput(room) {
		return ""
	}
	return room
}

func roomFormAddress(value string) string {
	value = strings.TrimSpace(value)
	if bareRoomInput(value) {
		return value
	}
	return invitationRoom(value)
}

func roomInvitationHref(address string) string {
	return "/?" + url.Values{"open": {"join-room"}, "room": {address}}.Encode()
}

func roomInvitationLinks(value string, found func(string)) (string, bool) {
	var out strings.Builder
	start := 0
	for _, match := range roomInvitationPattern.FindAllStringIndex(value, -1) {
		label := strings.TrimRight(value[match[0]:match[1]], ".,!?;:…")
		address := invitationRoom(label)
		if address == "" {
			continue
		}
		if found != nil {
			found(address)
		}
		href := roomInvitationHref(address)
		out.WriteString(html.EscapeString(value[start:match[0]]))
		out.WriteString(`<a href="` + html.EscapeString(href) + `" data-room-invitation="` + html.EscapeString(address) + `">` + html.EscapeString(label) + `</a>`)
		start = match[0] + len(label)
	}
	if start == 0 {
		return "", false
	}
	out.WriteString(html.EscapeString(value[start:]))
	return out.String(), true
}

func plainRoomLinks(value string) templ.Component {
	return templ.ComponentFunc(func(_ context.Context, w io.Writer) error {
		out, linked := roomInvitationLinks(value, nil)
		if !linked {
			out = html.EscapeString(value)
		}
		_, err := io.WriteString(w, out)
		return err
	})
}

func linkRenderedInvitations(rendered []byte, w io.Writer, found func(string)) error {
	tokens := htmlstream.NewTokenizer(bytes.NewReader(rendered))
	protected := 0
	for {
		kind := tokens.Next()
		if kind == htmlstream.ErrorToken {
			if tokens.Err() == io.EOF {
				return nil
			}
			return tokens.Err()
		}
		raw := tokens.Raw()
		switch kind {
		case htmlstream.StartTagToken, htmlstream.EndTagToken:
			name, _ := tokens.TagName()
			if string(name) == "code" || string(name) == "pre" || string(name) == "a" {
				if kind == htmlstream.StartTagToken {
					protected++
				} else {
					protected--
				}
			}
		case htmlstream.TextToken:
			if protected == 0 && bytes.Contains(bytes.ToLower(raw), []byte("xmpp:")) {
				// Text decoding may modify the tokenizer buffer.
				raw = bytes.Clone(raw)
				if linked, ok := roomInvitationLinks(string(tokens.Text()), found); ok {
					raw = []byte(linked)
				}
			}
		}
		if _, err := w.Write(raw); err != nil {
			return err
		}
	}
}
