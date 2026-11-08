package web

import "time"

func messageEdited(m messageView) bool { return m.Edited != nil && *m.Edited }
func messageEditedAt(m messageView) string {
	if messageEdited(m) && m.EditedAt != nil {
		return m.EditedAt.UTC().Format(time.RFC3339Nano)
	}
	return ""
}
