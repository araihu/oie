# Oie chat formatting feasibility

Checked 2026-11-06. The requested design is viable: use a restricted Margo-backed chat formatting profile and translate a protocol-neutral formatting representation in each adapter. Current Margo supplies the basic rendering primitives, but its public document API is not yet a complete chat-subset/transcoding API.

This is a feasibility finding, not an implemented Oie feature. Following the check, the user selected Margo's current syntax/parsing behavior and authorized public Margo API changes needed to expose formatting data for Oie. The accepted chat subset is bold, italic, strikethrough, inline/fenced code, links, lists, and blockquotes. Do not introduce a WhatsApp-specific dialect. Additive semantic-output API signatures remain implementation design work.

Subsequent product decisions: Oie attempts Markdown rendering by default, users can disable it, and message Copy always copies original text as `text/plain`, including Markdown markers. A separate rendered-text copy action with markers removed is explicitly backlogged.

## Local Margo evidence

Inspected `github.com/araihu/margo` in `/home/gui/repos/araihu/margo` at commit `4ff5caa8a434d8e1e9f18797fb5cda407dd8779c` (`v0.0.35-1-g4ff5caa`). The checkout was clean before and after the probe. It is one commit beyond tag `v0.0.35`; no released dependency version has been selected for Oie.

Executed a temporary standalone Go consumer using only the public `margo.New`, `Compile`, `Render`, and `RenderResult.Content` APIs. It uses a local module replacement, so it checks the current checkout rather than claiming a published-tag compatibility result. `go run -mod=readonly .` completed successfully; the supported inline primitives were asserted, and the remaining cases were inspected from the emitted results.

| Input/case | Observed behavior |
| --- | --- |
| `**bold** _italic_ ~~strike~~` and inline code | Renders `strong`, `em`, `del`, and `code` as expected. |
| WhatsApp-style `*bold* _italic_ ~strike~` | Single asterisks render as `em`, not bold; italic and single-tilde strike render. |
| Fenced code | Renders through the document code-block component, including document-oriented header/copy markup. |
| Heading | Produces a heading element and generated ID. |
| List | Produces list markup. |
| Raw script HTML | Default policy rejects compilation with `policy.raw_html.denied`. |
| `javascript:` Markdown link | Rendering rejects it with `render.link_invalid`. |
| Ordinary newline | HTML content contains a soft newline, requiring a deliberate chat line-break policy. |
| YAML-style frontmatter | Parses title metadata and removes frontmatter from visible content. |

Scratch artifacts: `/tmp/oie-formatting-probe/main.go`, `/tmp/oie-formatting-probe/go.mod`, `/tmp/oie-formatting-probe/results.jsonl`. No Margo or Goshtoso source was changed, and no real chat-server interoperability test was run.

Relevant public surfaces:

- `/home/gui/repos/araihu/margo/result.go`: `RenderResult.Content()` returns a `templ.Component`, suitable for composition inside a Goshtoso conversation.
- `/home/gui/repos/araihu/margo/document.go`: compiled `Document` representation is deliberately opaque; there is no exported formatting tree/span projection for protocol adapters.
- `/home/gui/repos/araihu/margo/extension.go`: public extension registration owns fences; it is not a general public override of the inline Markdown grammar.
- `/home/gui/repos/araihu/margo/policy.go`: HTML/resource policies do not themselves select a restricted Markdown feature grammar.

## Protocol mapping

| Target | Viable mapping and limitation |
| --- | --- |
| XMPP | XEP-0393 Message Styling provides emphasis, strong emphasis, strike, inline/preformatted code, and quotations. Style syntax differs from CommonMark. It requires no new server functionality; participating clients advertise support. Use this current styling extension rather than deprecated XHTML-IM for the initial design. |
| Matrix | Text messages can carry `body` and an optional `org.matrix.custom.html` `formatted_body`. Generate an allowed HTML projection plus a readable text fallback. |
| IRC | Map supported formatting spans to IRC control characters. Client/network handling and block/multiline limits vary; keep a readable plain-text fallback for unsupported structures. |
| Nostr private messages | NIP-17 kind-14 content is defined as plain text. Use readable plain-text fallback rather than promise interoperable rich formatting. Other event kinds/profiles would need their own checks later. |

Formatting is not uniformly a server capability. Depending on the protocol it is client-advertised, part of a message content format, a conventional text representation, or unavailable. Model effective formatting capabilities/formats at the adapter/account/conversation level without inventing one global server-formatting boolean.

## Recommended architecture

1. Use Margo's existing parser/syntax with the accepted Oie chat subset: bold/strong, italic/emphasis, strikethrough, inline/fenced code, links, lists, and blockquotes, alongside ordinary text/line breaks. This selection rules out a WhatsApp-specific alternate grammar and does not enable full document features in chat.
2. Add a public semantic-output projection where needed for protocol translation, without changing existing Margo compile/render behavior. It should yield supported text/spans/blocks plus readable text. Keep parser internals private; do not have adapters import `internal/` or scrape rendered HTML.
3. Use normalized formatting for Goshtoso rendering, plain-text search/notifications, and protocol encoding. The user chose Markdown presentation by default with a disable option; preserve native/authored source and provenance independently of that preference. Each provider adapter chooses its supported wire projection and readable fallback. Normal Copy preserves original message text and Markdown markers as `text/plain`; a separate rendered-text copy action is backlogged.
4. Retain authored source and its format/profile version with the logical message. Preserve native incoming formatting/provenance as needed. Formatting is content; application HTML is a presentation projection.
5. Make chat messages preserve visible newlines and code literally, while document frontmatter and unsupported document/runtime features cannot silently consume or execute message content. Treat unsupported syntax as readable text according to the chosen profile. Keep URL handling bounded to the allowed chat link policy.
6. Keep rich-link cards and binary media as their already-selected separate message features. Markdown inline image syntax must not bypass device media-download policy.
7. Apply the existing expression/i18n requirements to any chat code-block controls and accessible labels. Use escaped/safe rendering with the profile's allowlist; enabling unrestricted raw HTML is not required.

The focused prerequisite is a reusable public semantic-output/subset surface preserving Margo's existing behavior. The existing `Content()` component already provides Goshtoso composition. Exact new API design is not selected; do not rewrite Margo's parser or introduce a new chat dialect as part of this requirement.

## Selected syntax

The user explicitly said “keep margo current behaviour.” Preserve Margo's current Markdown parsing semantics, including `**bold**` for strong emphasis and `*italic*`/`_italic_` for emphasis. Use its existing strikethrough/code behavior. “Like WhatsApp” refers to a small useful chat-formatting feature set and presentation, not replacing Margo's delimiters. Each protocol adapter translates semantic formatting into its own supported wire representation; the composer source need not use that provider's delimiter syntax.

No WhatsApp-specific parser/normalizer or regex delimiter rewriting is selected. Any public structured-output API must be additive and preserve existing Margo behavior. The user authorizes needed Margo API work during integration; this pre-spec discussion has not yet started feature implementation.

## Primary references

- [XMPP Message Styling XEP-0393](https://xmpp.org/extensions/xep-0393.html)
- [Deprecated XHTML-IM XEP-0071](https://xmpp.org/extensions/xep-0071.html)
- [Matrix message formats](https://spec.matrix.org/latest/client-server-api/#mroommessage-msgtypes)
- [IRC formatting](https://modern.ircdocs.horse/formatting.html)
- [Nostr private messages NIP-17](https://github.com/nostr-protocol/nips/blob/master/17.md)
- [WhatsApp formatting syntax](https://faq.whatsapp.com/539178204879377/?cms_platform=web&locale=pt_BR)
