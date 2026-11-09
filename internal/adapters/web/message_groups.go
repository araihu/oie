package web

func messagesGrouped(a, b messageView) bool {
	return a.Attachment == nil && b.Attachment == nil &&
		a.AccountId == b.AccountId && a.ConversationId == b.ConversationId &&
		a.Sender.Id == b.Sender.Id && a.Sender.DisplayName == b.Sender.DisplayName &&
		a.IsOwn == b.IsOwn
}
