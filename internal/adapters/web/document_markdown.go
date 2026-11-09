package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/araihu/margo"
	"github.com/araihu/oie/internal/adapters/web/bffapi"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

const documentMarkdownLimit = 2 << 20

var documentMarkdownSlots = make(chan struct{}, 2)

// This operation only projects caller-supplied text; it never fetches a URL or
// changes engine data. Original-file streaming remains a separate operation.
func (s *Server) documentMarkdown(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		w.WriteHeader(405)
		return
	}
	kind, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || kind != "application/json" || r.URL.RawQuery != "" {
		w.WriteHeader(400)
		return
	}
	// JSON can escape each source byte into six ASCII bytes.
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 6*documentMarkdownLimit+1024))
	decoder.DisallowUnknownFields()
	var required struct {
		Source *string `json:"source"`
	}
	if err := decoder.Decode(&required); err != nil {
		var large *http.MaxBytesError
		if errors.As(err, &large) {
			w.WriteHeader(413)
		} else {
			w.WriteHeader(400)
		}
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		w.WriteHeader(400)
		return
	}
	if required.Source == nil {
		w.WriteHeader(400)
		return
	}
	input := bffapi.MarkdownSource{Source: *required.Source}
	if len(input.Source) > documentMarkdownLimit {
		w.WriteHeader(413)
		return
	}
	if !utf8.ValidString(input.Source) || strings.ContainsRune(input.Source, '\x00') {
		w.WriteHeader(400)
		return
	}
	select {
	case documentMarkdownSlots <- struct{}{}:
		defer func() { <-documentMarkdownSlots }()
	default:
		w.WriteHeader(503)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	fragment, err := renderDocumentMarkdown(ctx, input.Source)
	if err != nil {
		w.WriteHeader(422)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(bffapi.MarkdownFragment{Html: fragment})
}

func renderDocumentMarkdown(ctx context.Context, source string) (string, error) {
	policy := margo.DefaultPolicy()
	policy.RawHTML = margo.RawHTMLSanitized
	policy.InputBytes = documentMarkdownLimit
	policy.OutputBytes = 8 << 20
	// Source tags such as an unescaped <user> placeholder must not disable an
	// entire changelog. Only the static allowlist below is sent to the browser;
	// Margo's intermediate document HTML is never served or initialized.
	compiler := margo.New(margo.WithHostPolicy(policy), margo.WithUnsafeHTML())
	document, err := compiler.Compile(ctx, margo.Source{Name: "document.md", Content: []byte(source)})
	if err != nil {
		return "", err
	}
	rendered, err := compiler.Render(ctx, document)
	if err != nil {
		return "", err
	}
	projection, err := margo.RenderHTML(rendered)
	if err != nil {
		return "", err
	}
	var fragment bytes.Buffer
	if err := projection.Fragment().Render(ctx, &fragment); err != nil {
		return "", err
	}
	result, err := staticDocumentHTML(fragment.String())
	if err == nil && len(result) > 8<<20 {
		return "", errors.New("document output too large")
	}
	return result, err
}

// Margo owns Markdown parsing. Oie projects its HTML into a static host surface:
// no resource loads, document IDs, runtimes, authored CSS or active controls.
func staticDocumentHTML(fragment string) (string, error) {
	nodes, err := html.ParseFragment(strings.NewReader(fragment), &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div})
	if err != nil {
		return "", err
	}
	allowed := map[string]bool{}
	for _, name := range strings.Fields("article section div figure figcaption h1 h2 h3 h4 h5 h6 p ul ol li blockquote pre code span strong em del s hr br table thead tbody tfoot tr th td dl dt dd a sup sub") {
		allowed[name] = true
	}
	var clean func(*html.Node, *html.Node)
	clean = func(parent, node *html.Node) {
		if node.Type == html.TextNode {
			parent.AppendChild(&html.Node{Type: html.TextNode, Data: node.Data})
			return
		}
		if node.Type != html.ElementNode {
			return
		}
		if node.Data == "img" {
			for _, a := range node.Attr {
				if a.Key == "alt" {
					parent.AppendChild(&html.Node{Type: html.TextNode, Data: a.Val})
				}
			}
			return
		}
		if node.Data == "button" || node.Data == "script" || node.Data == "style" || node.Data == "iframe" || node.Data == "object" || node.Data == "embed" {
			return
		}
		target := parent
		if allowed[node.Data] {
			target = &html.Node{Type: html.ElementNode, Data: node.Data, DataAtom: node.DataAtom}
			if node.Data == "a" {
				for _, a := range node.Attr {
					if a.Key == "href" && safeDocumentLink(a.Val) {
						target.Attr = []html.Attribute{{Key: "href", Val: a.Val}, {Key: "target", Val: "_blank"}, {Key: "rel", Val: "noopener noreferrer"}}
					}
				}
			}
			parent.AppendChild(target)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			clean(target, child)
		}
	}
	root := &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div}
	for _, node := range nodes {
		clean(root, node)
	}
	var result strings.Builder
	for child := root.FirstChild; child != nil; child = child.NextSibling {
		if err := html.Render(&result, child); err != nil {
			return "", err
		}
	}
	return result.String(), nil
}
func safeDocumentLink(address string) bool {
	parsed, err := url.Parse(address)
	return err == nil && parsed.User == nil && ((parsed.Scheme == "https" || parsed.Scheme == "http") && parsed.Host != "" || parsed.Scheme == "mailto" && parsed.Opaque != "")
}
