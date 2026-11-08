# Media gallery scrolling

The global Media and Links gallery and the conversation's media panel load older items as the user approaches the bottom. The existing engine cursor API supplies each page. The BFF returns HTML fragments, so no new engine operation or browser protocol client is needed.

Pages append to the current collection without adding a cursor to browser history. Account, conversation, category and search filters remain in effect. Month headings use the device timezone. A native pagination link remains available without JavaScript and becomes an explicit retry after a failed request.

Each response must match the engine, collection and requested cursor. The browser checks reference identities and chronological order before accepting it. Changing the collection or closing the conversation panel cancels its pending request. Existing items remain visible after a failed page load.

Live updates reconcile the current page with the loaded older items. Older windows are rechecked at a paced rate, so changes outside the current page can take another refresh to appear. Active video elements and unchanged cards remain connected during updates. Returning through browser history can restore the loaded depth and scroll position from a bounded, temporary tab cache. That cache strips active media sources and is not offline storage.

The gallery does not download full videos automatically. Image download preferences still apply.
