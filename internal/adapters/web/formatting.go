package web

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"slices"
	"strings"

	"github.com/a-h/templ"
	"github.com/araihu/margo"
	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

func formattedMessage(ctx context.Context, original string) templ.Component {
	component, _ := formattedMessageWithInvitations(ctx, original)
	return component
}

func formattedMessageWithInvitations(ctx context.Context, original string) (templ.Component, []string) {
	content, err := margo.RenderChatWithOptions(ctx, original, margo.ChatOptions{ExplicitBlockquotes: true})
	if err != nil {
		return nil, nil
	}
	if !strings.Contains(strings.ToLower(original), "xmpp:") {
		return content, nil
	}
	var rendered, linked bytes.Buffer
	if err := content.Render(ctx, &rendered); err != nil {
		return nil, nil
	}
	var rooms []string
	if err := linkRenderedInvitations(rendered.Bytes(), &linked, func(address string) {
		if len(rooms) < 3 && !slices.Contains(rooms, address) {
			rooms = append(rooms, address)
		}
	}); err != nil {
		return content, nil
	}
	result := linked.String()
	return templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		_, err := io.WriteString(w, result)
		return err
	}), rooms
}

func originalMessageJSON(original string) string {
	data, _ := json.Marshal(original)
	return string(data)
}

// imagePresentation removes only standalone image-reference lines represented
// by a structured preview. It is a display projection, never message editing or
// Markdown parsing. Conservative code guards keep examples and captions intact.
func imagePresentation(original string, previews []engineapi.MessagePreview) string {
	images := map[string]bool{}
	for _, p := range previews {
		if p.Kind == "image" {
			images[p.Url] = true
		}
	}
	return referencePresentation(original, images)
}

func referencePresentation(original string, images map[string]bool) string {
	if len(images) == 0 {
		return original
	}
	lines := strings.SplitAfter(original, "\n")
	var out strings.Builder
	removed := false
	var fence byte
	fenceLength, inlineTicks := 0, 0
	for _, line := range lines {
		marker, count, tail := presentationFence(line)
		if fence != 0 {
			out.WriteString(line)
			if marker == fence && count >= fenceLength && strings.TrimSpace(tail) == "" {
				fence = 0
				fenceLength = 0
			}
			continue
		}
		if marker != 0 && inlineTicks == 0 {
			fence = marker
			fenceLength = count
			out.WriteString(line)
			continue
		}
		trimmed := strings.TrimSpace(line)
		indented := strings.HasPrefix(line, "    ") || strings.HasPrefix(strings.TrimLeft(line, " "), "\t")
		if inlineTicks == 0 && !indented && images[trimmed] {
			removed = true
			continue
		}
		out.WriteString(line)
		inlineTicks = presentationInlineTicks(line, inlineTicks)
	}
	if !removed {
		return original
	}
	result := out.String()
	if strings.TrimSpace(result) == "" {
		return ""
	}
	return result
}
func presentationFence(line string) (byte, int, string) {
	// Prefix stripping deliberately over-protects quoted/list fences. A line with
	// ordinary prose before the marker cannot become an image-only candidate.
	rest := strings.TrimLeft(line, " \t>+-*.0123456789)")
	if len(rest) == 0 || (rest[0] != '`' && rest[0] != '~') {
		return 0, 0, ""
	}
	marker := rest[0]
	count := 0
	for count < len(rest) && rest[count] == marker {
		count++
	}
	if count < 3 {
		return 0, 0, ""
	}
	return marker, count, rest[count:]
}
func presentationInlineTicks(line string, current int) int {
	for i := 0; i < len(line); {
		if line[i] == '\\' {
			i += 2
			continue
		}
		if line[i] != '`' {
			i++
			continue
		}
		end := i + 1
		for end < len(line) && line[end] == '`' {
			end++
		}
		length := end - i
		switch current {
		case 0:
			current = length
		case length:
			current = 0
		}
		i = end
	}
	return current
}

func (m messageView) presentationText() string {
	if !m.PresentationKnown && m.Presentation == "" {
		return m.displayBody()
	}
	return m.Presentation
}
