# Explicit chat quote boundaries

The reported message had a quoted line followed immediately by an unprefixed emoji response. CommonMark permits lazy paragraph continuation, so both lines appeared inside the quote. Oie now opts into explicit quote boundaries through Margo's public `RenderChatWithOptions` API and `ChatOptions{ExplicitBlockquotes: true}`.

For example, `> source\n🙌` displays the emoji outside the quote. `>> inner\n> outer\nresponse` has nesting depths two, one, and zero. Fenced code and inline code keep literal markers. The stored source and Copy original action keep the original bytes, including CRLF line endings. The plaintext fallback and disabled-rendering mode display the original markers, with normal browser newline handling.

The generic parser option is published in [Margo PR 119](https://github.com/araihu/margo/pull/119), stacked on the chat renderer PR. Oie pins `v0.0.36-0.20261108235149-ff44a5fe5052`. Margo's existing `RenderChat`, `Compile`, and `Render` defaults remain unchanged. Oie does not rewrite message text or import parser internals.

Native regression tests inspect rendered blockquote depth and exact copy source. Browser fixtures cover quote boundaries, nesting, CRLF and Unicode copy, disabled rendering, literal code, and incoming-message refreshes. Existing protocol reply tests remain separate because this change handles Markdown quote presentation, not reply metadata or message delivery.
