package render

import (
	"strings"
	"sync"
	"testing"
)

func mustRender(t *testing.T, docPath, src string) Result {
	t.Helper()
	res, err := New().Render(docPath, []byte(src))
	if err != nil {
		t.Fatalf("Render(%q) error: %v", docPath, err)
	}
	return res
}

func assertContains(t *testing.T, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Errorf("output missing %q:\n%s", want, got)
	}
}

func assertNotContains(t *testing.T, got, unwanted string) {
	t.Helper()
	if strings.Contains(got, unwanted) {
		t.Errorf("output unexpectedly contains %q:\n%s", unwanted, got)
	}
}

func TestGFMElements(t *testing.T) {
	src := "| A | B |\n| --- | --- |\n| 1 | 2 |\n\n" +
		"~~gone~~\n\n" +
		"- [x] done\n- [ ] todo\n\n" +
		"<https://example.com>\n\n" +
		"```go\nfmt.Println()\n```\n"

	res := mustRender(t, "doc.md", src)

	for _, want := range []string{
		"<table>",
		"<thead>",
		"<th",
		"<td",
		"<del>gone</del>",
		`type="checkbox"`,
		"disabled",
		"checked",
		`<a href="https://example.com"`,
		"https://example.com</a>",
		`class="language-go"`,
	} {
		assertContains(t, res.HTML, want)
	}
}

func TestHeadingsAndTitle(t *testing.T) {
	src := "# Hello World\n\n## Second *em* `code`\n\n### Third\n"

	res := mustRender(t, "doc.md", src)

	if res.Title != "Hello World" {
		t.Errorf("Title = %q, want %q", res.Title, "Hello World")
	}
	want := []Heading{
		{Level: 1, Text: "Hello World", ID: "hello-world"},
		{Level: 2, Text: "Second em code", ID: "second-em-code"},
		{Level: 3, Text: "Third", ID: "third"},
	}
	if len(res.Headings) != len(want) {
		t.Fatalf("Headings = %#v, want %#v", res.Headings, want)
	}
	for i := range want {
		if res.Headings[i] != want[i] {
			t.Errorf("Headings[%d] = %#v, want %#v", i, res.Headings[i], want[i])
		}
	}

	assertContains(t, res.HTML, `id="hello-world"`)

	noH1 := mustRender(t, "doc.md", "## Only a second level\n")
	if noH1.Title != "" {
		t.Errorf("Title = %q, want empty", noH1.Title)
	}
}

func TestUnicodeHeadingAndLink(t *testing.T) {
	src := "# Café 日本語\n\n[link](日本語.md)\n"

	res := mustRender(t, "b.md", src)

	if res.Title != "Café 日本語" {
		t.Errorf("Title = %q, want %q", res.Title, "Café 日本語")
	}
	assertContains(t, res.HTML, `/read/%E6%97%A5%E6%9C%AC%E8%AA%9E.md`)
}

func TestFrontmatter(t *testing.T) {
	res := mustRender(t, "doc.md", "---\ntitle: hidden\n---\n# Body\n")
	assertNotContains(t, res.HTML, "title: hidden")
	assertContains(t, res.HTML, "Body")

	res = mustRender(t, "doc.md", "---\ntitle: visible\n# Body\n")
	assertContains(t, res.HTML, "title: visible")

	res = mustRender(t, "doc.md", "---\r\ntitle: hidden\r\n...\r\n# Body\r\n")
	assertNotContains(t, res.HTML, "title: hidden")
	assertContains(t, res.HTML, "Body")
}

func TestRelativeLinks(t *testing.T) {
	src := "[a](foo.md)\n\n[b](../top.md)\n\n[c](foo.md#x)\n"

	res := mustRender(t, "dir/doc.md", src)

	assertContains(t, res.HTML, `href="/read/dir/foo.md"`)
	assertContains(t, res.HTML, `href="/read/top.md"`)
	assertContains(t, res.HTML, `href="/read/dir/foo.md#x"`)
}

func TestRelativeImages(t *testing.T) {
	src := "![x](<img/a b.png>)\n\n![y](/x.png)\n\n![z](https://e/x.png)\n\n[a](#anchor)\n"

	res := mustRender(t, "dir/doc.md", src)

	assertContains(t, res.HTML, `src="/assets/dir/img/a%20b.png"`)
	assertContains(t, res.HTML, `src="/x.png"`)
	assertContains(t, res.HTML, `src="https://e/x.png"`)
	assertContains(t, res.HTML, `href="#anchor"`)
}

func TestPathEscapeLeavesClimbingPathUnchanged(t *testing.T) {
	res := mustRender(t, "dir/doc.md", "[p](../../etc/passwd)\n")

	assertContains(t, res.HTML, `href="../../etc/passwd"`)
	assertNotContains(t, res.HTML, "/read/")
}

func TestXSSIsNeutralized(t *testing.T) {
	src := "<script>alert(1)</script>\n\n" +
		"<img src=x onerror=alert(1)>\n\n" +
		"[x](javascript:alert(1))\n\n" +
		"<iframe src=\"https://e\"></iframe>\n\n" +
		"<div style=\"color:red\">hi</div>\n\n" +
		"<a href=\"https://e\" target=\"_blank\">t</a>\n"

	res := mustRender(t, "doc.md", src)

	for _, bad := range []string{
		"<script",
		"</script",
		"<iframe",
		"onerror",
		"javascript:",
		"style=",
		"target=",
		"alert(1)",
	} {
		assertNotContains(t, res.HTML, bad)
	}
	assertContains(t, res.HTML, "hi")
	assertContains(t, res.HTML, ">t</a>")
}

func TestConcurrentRender(t *testing.T) {
	r := New()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := r.Render("dir/doc.md", []byte("# Title\n\n[link](foo.md)\n"))
			if err != nil {
				t.Errorf("Render error: %v", err)
				return
			}
			if res.Title != "Title" {
				t.Errorf("Title = %q, want %q", res.Title, "Title")
			}
			if !strings.Contains(res.HTML, "/read/dir/foo.md") {
				t.Errorf("HTML missing rewritten link:\n%s", res.HTML)
			}
		}()
	}
	wg.Wait()
}

func TestPercentEncodedLinks(t *testing.T) {
	src := "[a](Other%20Doc.md)\n\n[b](Caf%C3%A9.md)\n"

	res := mustRender(t, "dir/doc.md", src)

	assertContains(t, res.HTML, `href="/read/dir/Other%20Doc.md"`)
	assertContains(t, res.HTML, `href="/read/dir/Caf%C3%A9.md"`)
	assertNotContains(t, res.HTML, "%2520")

	if got := string(rewriteDestination([]byte("100%zz.md"), "dir")); got != "100%zz.md" {
		t.Errorf("invalid escape = %q, want unchanged", got)
	}
}

func TestBrowseAndUnknownTargets(t *testing.T) {
	src := "[Sub](sub/)\n\n[Up](../top)\n\n[Pdf](file.pdf)\n"

	res := mustRender(t, "dir/doc.md", src)

	assertContains(t, res.HTML, `href="/browse/dir/sub"`)
	assertContains(t, res.HTML, `href="/browse/top"`)
	assertContains(t, res.HTML, `href="file.pdf"`)
	assertNotContains(t, res.HTML, `href="/read/file.pdf"`)
}

func TestHeadingIDScopeAndValidation(t *testing.T) {
	src := "# Good Section\n\n[toc](#good-section)\n\n" +
		"<div id=\"x\">x</div>\n\n" +
		"<h1 id=\"bad id\">Bad</h1>\n\n" +
		"<h2 id=\"keep-1\">Keep</h2>\n"

	res := mustRender(t, "doc.md", src)

	assertContains(t, res.HTML, `<h1 id="good-section">`)
	assertContains(t, res.HTML, `<h2 id="keep-1">`)
	assertContains(t, res.HTML, `href="#good-section"`)
	assertNotContains(t, res.HTML, `id="x"`)
	assertNotContains(t, res.HTML, `id="bad id"`)
}
