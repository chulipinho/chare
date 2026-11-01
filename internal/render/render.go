package render

import (
	"bytes"
	"fmt"
	"html"
	"net/url"
	"path"
	"regexp"
	"strings"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	gmhtml "github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
	nethtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

type Heading struct {
	Level int
	Text  string
	ID    string
}

type Result struct {
	HTML     string
	Title    string
	Headings []Heading
}

type Renderer struct {
	markdown goldmark.Markdown
	policy   *bluemonday.Policy
}

func New() *Renderer {
	p := bluemonday.UGCPolicy()

	p.AllowAttrs("id").
		Matching(regexp.MustCompile(`^[\p{L}\p{N}_:.-]+$`)).
		OnElements("h1", "h2", "h3", "h4", "h5", "h6")
	p.AllowAttrs("class").
		Matching(regexp.MustCompile(`^language-[a-z0-9+#-]+$`)).
		OnElements("code")
	p.AllowElements("input")
	p.AllowAttrs("type").
		Matching(regexp.MustCompile(`^checkbox$`)).
		OnElements("input")
	p.AllowAttrs("checked", "disabled").OnElements("input")
	p.AllowAttrs("align").
		Matching(regexp.MustCompile(`(?i)^(left|right|center|justify)$`)).
		OnElements("th", "td")
	p.AllowElements("table", "thead", "tbody", "tfoot", "tr", "th", "td", "del", "s")

	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithParserOptions(
			parser.WithAutoHeadingID(),
			parser.WithASTTransformers(util.Prioritized(linkRewriter{}, 100)),
		),
		goldmark.WithRendererOptions(
			gmhtml.WithUnsafe(),
		),
	)

	return &Renderer{markdown: md, policy: p}
}

func (r *Renderer) Render(docPath string, src []byte) (Result, error) {
	src = stripFrontmatter(src)

	ctx := parser.NewContext()
	ctx.Set(docPathKey, docPath)

	reader := text.NewReader(src)
	doc := r.markdown.Parser().Parse(reader, parser.WithContext(ctx))

	var buf bytes.Buffer
	if err := r.markdown.Renderer().Render(&buf, src, doc); err != nil {
		return Result{}, fmt.Errorf("render markdown: %w", err)
	}

	sanitized, err := sanitizeIDs(string(r.policy.SanitizeBytes(buf.Bytes())))
	if err != nil {
		return Result{}, fmt.Errorf("sanitize ids: %w", err)
	}

	res := Result{HTML: sanitized}
	titleSet := false

	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		h, ok := n.(*ast.Heading)
		if !ok {
			return ast.WalkContinue, nil
		}
		heading := Heading{
			Level: h.Level,
			Text:  headingText(h, src),
			ID:    headingID(h),
		}
		res.Headings = append(res.Headings, heading)
		if h.Level == 1 && !titleSet {
			res.Title = heading.Text
			titleSet = true
		}
		return ast.WalkContinue, nil
	})

	return res, nil
}

var docPathKey = parser.NewContextKey()

type linkRewriter struct{}

func (linkRewriter) Transform(doc *ast.Document, _ text.Reader, pc parser.Context) {
	docPath, _ := pc.Get(docPathKey).(string)
	base := path.Dir(docPath)

	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n := n.(type) {
		case *ast.Link:
			n.Destination = rewriteDestination(n.Destination, base)
		case *ast.Image:
			n.Destination = rewriteDestination(n.Destination, base)
		}
		return ast.WalkContinue, nil
	})
}

func rewriteDestination(dest []byte, base string) []byte {
	if len(dest) == 0 {
		return dest
	}
	s := string(dest)
	if strings.HasPrefix(s, "#") || strings.HasPrefix(s, "/") {
		return dest
	}
	if u, err := url.Parse(s); err == nil && u.Scheme != "" {
		return dest
	}

	pathPart, suffix := s, ""
	if i := strings.IndexAny(s, "?#"); i >= 0 {
		pathPart, suffix = s[:i], s[i:]
	}
	if pathPart == "" {
		return dest
	}

	unescaped, err := url.PathUnescape(pathPart)
	if err != nil {
		return dest
	}

	resolved := path.Clean(path.Join(base, unescaped))
	if resolved == ".." || strings.HasPrefix(resolved, "../") {
		return dest
	}

	ext := strings.ToLower(path.Ext(resolved))
	switch ext {
	case ".md", ".markdown", ".txt":
		return []byte("/read/" + escapePath(resolved) + suffix)
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return []byte("/assets/" + escapePath(resolved) + suffix)
	}
	if strings.HasSuffix(unescaped, "/") || ext == "" {
		return []byte("/browse/" + escapePath(resolved) + suffix)
	}
	return dest
}

func escapePath(p string) string {
	segments := strings.Split(p, "/")
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}
	return strings.Join(segments, "/")
}

var headingIDPattern = regexp.MustCompile(`^[\p{L}\p{N}_:.-]+$`)

func sanitizeIDs(fragment string) (string, error) {
	nodes, err := nethtml.ParseFragment(strings.NewReader(fragment), &nethtml.Node{
		Type:     nethtml.ElementNode,
		DataAtom: atom.Div,
		Data:     "div",
	})
	if err != nil {
		return "", err
	}

	var b strings.Builder
	for _, n := range nodes {
		stripDisallowedIDs(n)
		if err := nethtml.Render(&b, n); err != nil {
			return "", err
		}
	}
	return b.String(), nil
}

func stripDisallowedIDs(n *nethtml.Node) {
	if n.Type == nethtml.ElementNode {
		if id, ok := attrValue(n, "id"); ok && !headingElement(n.Data) {
			removeAttr(n, "id")
		} else if ok && !headingIDPattern.MatchString(id) {
			removeAttr(n, "id")
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		stripDisallowedIDs(c)
	}
}

func headingElement(name string) bool {
	switch name {
	case "h1", "h2", "h3", "h4", "h5", "h6":
		return true
	default:
		return false
	}
}

func attrValue(n *nethtml.Node, key string) (string, bool) {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val, true
		}
	}
	return "", false
}

func removeAttr(n *nethtml.Node, key string) {
	for i, a := range n.Attr {
		if a.Key == key {
			n.Attr = append(n.Attr[:i], n.Attr[i+1:]...)
			return
		}
	}
}

func headingText(n ast.Node, src []byte) string {
	var b strings.Builder
	_ = ast.Walk(n, func(child ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch t := child.(type) {
		case *ast.Text:
			b.Write(t.Segment.Value(src))
		case *ast.String:
			b.Write(t.Value)
		}
		return ast.WalkContinue, nil
	})
	return html.UnescapeString(b.String())
}

func headingID(n ast.Node) string {
	for _, attr := range n.Attributes() {
		if string(attr.Name) != "id" {
			continue
		}
		switch v := attr.Value.(type) {
		case []byte:
			return string(v)
		case string:
			return v
		default:
			return fmt.Sprint(v)
		}
	}
	return ""
}

func stripFrontmatter(src []byte) []byte {
	if !bytes.HasPrefix(src, []byte("---\n")) && !bytes.HasPrefix(src, []byte("---\r\n")) {
		return src
	}

	rest := src[bytes.IndexByte(src, '\n')+1:]
	for {
		j := bytes.IndexByte(rest, '\n')
		var line, next []byte
		if j < 0 {
			line = rest
		} else {
			line = rest[:j]
			next = rest[j+1:]
		}
		line = bytes.TrimSuffix(line, []byte("\r"))
		if bytes.Equal(line, []byte("---")) || bytes.Equal(line, []byte("...")) {
			return next
		}
		if j < 0 {
			return src
		}
		rest = next
	}
}
