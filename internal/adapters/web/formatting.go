package web

import (
	"context"
	"encoding/json"

	"github.com/a-h/templ"
	"github.com/araihu/margo"
)

func formattedMessage(ctx context.Context, original string) templ.Component {
	content, err := margo.RenderChat(ctx, original)
	if err != nil {
		return nil
	}
	return content
}
func originalMessageJSON(original string) string {
	data, _ := json.Marshal(original)
	return string(data)
}
