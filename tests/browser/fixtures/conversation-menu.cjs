function conversationMenuTrigger(page) {
  return page.locator('#chat-options [aria-controls="chat-options-panel"]');
}

async function openConversationDetails(page) {
  await conversationMenuTrigger(page).click();
  await page.locator('#chat-conversation-details').click();
}

module.exports = {conversationMenuTrigger, openConversationDetails};
