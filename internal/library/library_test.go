package library

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newLibrary(t *testing.T) *Library {
	t.Helper()
	lib, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return lib
}

func writeFile(t *testing.T, lib *Library, rel, content string) {
	t.Helper()
	if _, err := lib.Write(rel, []byte(content), true); err != nil {
		t.Fatalf("Write(%q): %v", rel, err)
	}
}

func mkdir(t *testing.T, lib *Library, rel string) {
	t.Helper()
	if _, err := lib.Mkdir(rel); err != nil {
		t.Fatalf("Mkdir(%q): %v", rel, err)
	}
}

func wantErr(t *testing.T, err, target error, what string) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("%s: got %v, want %v", what, err, target)
	}
}

func TestOpen(t *testing.T) {
	root := filepath.Join(t.TempDir(), "nested", "library")
	lib, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !filepath.IsAbs(lib.Root()) {
		t.Fatalf("Root() = %q, want absolute", lib.Root())
	}
	fi, err := os.Stat(lib.Root())
	if err != nil || !fi.IsDir() {
		t.Fatalf("root is not a directory: %v", err)
	}

	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(file); err == nil {
		t.Fatal("Open on a file succeeded")
	}
}

func TestValidateRel(t *testing.T) {
	lib := newLibrary(t)
	invalid := []string{
		"/abs",
		"/etc/passwd",
		"..",
		"../x",
		"a/../b",
		".hidden",
		"a/.hidden",
		"a//b",
		"a/",
		"a\\b",
		"\\x",
		"C:/x",
		"c:foo",
		"a\x01b",
		"a\x00b",
		strings.Repeat("a", 1025),
	}
	for _, rel := range invalid {
		_, err := lib.Stat(rel)
		if !errors.Is(err, ErrInvalidPath) {
			t.Errorf("Stat(%q): got %v, want ErrInvalidPath", rel, err)
		}
	}

	if _, err := lib.Stat(""); err != nil {
		t.Errorf("Stat(\"\"): %v", err)
	}
}

func TestKindFor(t *testing.T) {
	tests := map[string]Kind{
		"a.md":         KindDocument,
		"a.MD":         KindDocument,
		"a.markdown":   KindDocument,
		"a.txt":        KindDocument,
		"a.png":        KindAsset,
		"a.JPG":        KindAsset,
		"a.jpeg":       KindAsset,
		"a.gif":        KindAsset,
		"a.webp":       KindAsset,
		"a.html":       "",
		"a":            "",
		"a.md.txt":     KindDocument,
		"a.png.md":     KindDocument,
		"a.weird":      "",
		"archive.tar":  "",
		"picture.webp": KindAsset,
	}
	for name, want := range tests {
		if got := KindFor(name); got != want {
			t.Errorf("KindFor(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestList(t *testing.T) {
	lib := newLibrary(t)
	for _, dir := range []string{"alpha", "Beta", "Zed"} {
		mkdir(t, lib, dir)
	}
	if err := os.Mkdir(filepath.Join(lib.Root(), ".hiddenDir"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, lib, "A.md", "a")
	writeFile(t, lib, "b.txt", "b")
	writeFile(t, lib, "pic.PNG", "png")
	if err := os.WriteFile(filepath.Join(lib.Root(), ".secret.md"), []byte("hidden"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lib.Root(), "note.unsupported"), []byte("nope"), 0o644); err != nil {
		t.Fatal(err)
	}

	symlinked := true
	if err := os.Symlink(filepath.Join(lib.Root(), "A.md"), filepath.Join(lib.Root(), "link.md")); err != nil {
		symlinked = false
	}

	entries, err := lib.List("")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name)
	}
	want := []string{"alpha", "Beta", "Zed", "A.md", "b.txt", "pic.PNG"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("List order = %v, want %v", names, want)
	}
	for _, entry := range entries {
		if entry.Name == "alpha" || entry.Name == "Beta" || entry.Name == "Zed" {
			if entry.Kind != KindDir {
				t.Errorf("%q kind = %q, want dir", entry.Name, entry.Kind)
			}
			if entry.Size != 0 {
				t.Errorf("%q size = %d, want 0", entry.Name, entry.Size)
			}
		}
	}
	if symlinked {
		for _, name := range names {
			if name == "link.md" {
				t.Fatal("List included a symlink")
			}
		}
	}

	if _, err := lib.List("A.md"); !errors.Is(err, ErrNotDir) {
		t.Fatalf("List file: got %v, want ErrNotDir", err)
	}
	if _, err := lib.List("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("List missing: got %v, want ErrNotFound", err)
	}
}

func TestStat(t *testing.T) {
	lib := newLibrary(t)
	mkdir(t, lib, "dir")
	writeFile(t, lib, "doc.md", "hello")
	writeFile(t, lib, "image.png", "png")
	if err := os.WriteFile(filepath.Join(lib.Root(), "bad.zzz"), []byte("zzz"), 0o644); err != nil {
		t.Fatal(err)
	}

	root, err := lib.Stat("")
	if err != nil {
		t.Fatalf("Stat root: %v", err)
	}
	if root.Path != "" || root.Name != "" || root.Kind != KindDir {
		t.Fatalf("root = %+v", root)
	}

	doc, err := lib.Stat("doc.md")
	if err != nil {
		t.Fatalf("Stat doc: %v", err)
	}
	if doc.Name != "doc.md" || doc.Path != "doc.md" || doc.Kind != KindDocument || doc.Size != 5 {
		t.Fatalf("doc = %+v", doc)
	}

	asset, err := lib.Stat("image.png")
	if err != nil {
		t.Fatalf("Stat asset: %v", err)
	}
	if asset.Kind != KindAsset {
		t.Fatalf("asset kind = %q", asset.Kind)
	}

	dir, err := lib.Stat("dir")
	if err != nil {
		t.Fatalf("Stat dir: %v", err)
	}
	if dir.Kind != KindDir {
		t.Fatalf("dir kind = %q", dir.Kind)
	}

	if _, err := lib.Stat("bad.zzz"); !errors.Is(err, ErrUnsupportedType) {
		t.Fatalf("Stat unsupported: got %v", err)
	}
	if _, err := lib.Stat("missing.md"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Stat missing: got %v", err)
	}
}

func TestRead(t *testing.T) {
	lib := newLibrary(t)
	mkdir(t, lib, "dir")
	writeFile(t, lib, "doc.md", "hello")
	writeFile(t, lib, "image.png", "png")
	if err := os.WriteFile(filepath.Join(lib.Root(), "bad.zzz"), []byte("zzz"), 0o644); err != nil {
		t.Fatal(err)
	}

	data, entry, err := lib.Read("doc.md")
	if err != nil {
		t.Fatalf("Read doc: %v", err)
	}
	if string(data) != "hello" || entry.Kind != KindDocument || entry.Size != 5 {
		t.Fatalf("Read doc = %q, %+v", data, entry)
	}
	data[0] = 'x'
	again, _, err := lib.Read("doc.md")
	if err != nil {
		t.Fatalf("Read doc again: %v", err)
	}
	if string(again) != "hello" {
		t.Fatalf("Read returned aliased data: %q", again)
	}

	data, entry, err = lib.Read("image.png")
	if err != nil {
		t.Fatalf("Read asset: %v", err)
	}
	if string(data) != "png" || entry.Kind != KindAsset {
		t.Fatalf("Read asset = %q, %+v", data, entry)
	}

	if _, _, err := lib.Read("dir"); !errors.Is(err, ErrIsDir) {
		t.Fatalf("Read dir: got %v", err)
	}
	if _, _, err := lib.Read(""); !errors.Is(err, ErrIsDir) {
		t.Fatalf("Read root: got %v", err)
	}
	if _, _, err := lib.Read("bad.zzz"); !errors.Is(err, ErrUnsupportedType) {
		t.Fatalf("Read unsupported: got %v", err)
	}
	if _, _, err := lib.Read("missing.md"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Read missing: got %v", err)
	}
}

func TestWrite(t *testing.T) {
	lib := newLibrary(t)
	mkdir(t, lib, "docs")
	mkdir(t, lib, "folder.md")
	mkdir(t, lib, "plain")

	entry, err := lib.Write("docs/new.md", []byte("one"), false)
	if err != nil {
		t.Fatalf("Write create: %v", err)
	}
	if entry.Path != "docs/new.md" || entry.Kind != KindDocument || entry.Size != 3 {
		t.Fatalf("entry = %+v", entry)
	}
	data, _, err := lib.Read("docs/new.md")
	if err != nil || string(data) != "one" {
		t.Fatalf("Read after create = %q, %v", data, err)
	}

	if _, err := lib.Write("docs/new.md", []byte("two"), false); !errors.Is(err, ErrExists) {
		t.Fatalf("Write collision: got %v", err)
	}
	data, _, _ = lib.Read("docs/new.md")
	if string(data) != "one" {
		t.Fatalf("collision changed content: %q", data)
	}

	if _, err := lib.Write("docs/new.md", []byte("two"), true); err != nil {
		t.Fatalf("Write overwrite: %v", err)
	}
	data, _, _ = lib.Read("docs/new.md")
	if string(data) != "two" {
		t.Fatalf("overwrite content = %q", data)
	}

	if _, err := lib.Write("missing/a.md", []byte("x"), true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Write missing parent: got %v", err)
	}
	if _, err := lib.Write("docs/new.md/child.md", []byte("x"), true); !errors.Is(err, ErrNotDir) {
		t.Fatalf("Write file parent: got %v", err)
	}
	if _, err := lib.Write("folder.md", []byte("x"), true); !errors.Is(err, ErrIsDir) {
		t.Fatalf("Write dir target: got %v", err)
	}
	if _, err := lib.Write("plain", []byte("x"), true); !errors.Is(err, ErrIsDir) {
		t.Fatalf("Write plain dir target: got %v", err)
	}
	if _, err := lib.Write("bad.zzz", []byte("x"), true); !errors.Is(err, ErrUnsupportedType) {
		t.Fatalf("Write unsupported: got %v", err)
	}
	if _, err := lib.Write("", []byte("x"), true); !errors.Is(err, ErrUnsupportedType) {
		t.Fatalf("Write root: got %v", err)
	}

	entries, err := os.ReadDir(filepath.Join(lib.Root(), "docs"))
	if err != nil {
		t.Fatal(err)
	}
	for _, de := range entries {
		if strings.HasPrefix(de.Name(), ".chare-tmp-") {
			t.Fatalf("leftover temp file: %q", de.Name())
		}
	}
}

func TestRename(t *testing.T) {
	lib := newLibrary(t)
	mkdir(t, lib, "dir")
	writeFile(t, lib, "a.md", "a")
	writeFile(t, lib, "c.md", "c")
	if err := os.WriteFile(filepath.Join(lib.Root(), "bad.zzz"), []byte("bad"), 0o644); err != nil {
		t.Fatal(err)
	}

	entry, err := lib.Rename("a.md", "b.md", false)
	if err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if entry.Path != "b.md" || entry.Kind != KindDocument {
		t.Fatalf("entry = %+v", entry)
	}
	if _, err := lib.Stat("a.md"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("source still exists: %v", err)
	}
	data, _, err := lib.Read("b.md")
	if err != nil || string(data) != "a" {
		t.Fatalf("renamed content = %q, %v", data, err)
	}

	if _, err := lib.Rename("b.md", "c.md", false); !errors.Is(err, ErrExists) {
		t.Fatalf("Rename collision: got %v", err)
	}
	source, _, err := lib.Read("b.md")
	if err != nil || string(source) != "a" {
		t.Fatalf("collision changed source = %q, %v", source, err)
	}
	dest, _, err := lib.Read("c.md")
	if err != nil || string(dest) != "c" {
		t.Fatalf("collision changed destination = %q, %v", dest, err)
	}
	if _, err := lib.Rename("b.md", "c.md", true); err != nil {
		t.Fatalf("Rename overwrite: %v", err)
	}
	data, _, _ = lib.Read("c.md")
	if string(data) != "a" {
		t.Fatalf("overwrite content = %q", data)
	}

	if _, err := lib.Rename("dir", "dir2", false); !errors.Is(err, ErrIsDir) {
		t.Fatalf("Rename dir: got %v", err)
	}
	if _, err := lib.Rename("missing.md", "x.md", false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Rename missing: got %v", err)
	}
	if _, err := lib.Rename("c.md", "missing/x.md", false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Rename missing parent: got %v", err)
	}
	if _, err := lib.Rename("bad.zzz", "x.md", false); !errors.Is(err, ErrUnsupportedType) {
		t.Fatalf("Rename unsupported: got %v", err)
	}
	if _, err := lib.Rename("c.md", "dir", false); !errors.Is(err, ErrIsDir) {
		t.Fatalf("Rename onto dir: got %v", err)
	}
}

func TestDelete(t *testing.T) {
	lib := newLibrary(t)
	mkdir(t, lib, "dir")
	writeFile(t, lib, "doc.md", "x")

	if err := lib.Delete("doc.md"); err != nil {
		t.Fatalf("Delete file: %v", err)
	}
	if _, err := lib.Stat("doc.md"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("file still exists: %v", err)
	}
	if err := lib.Delete("dir"); !errors.Is(err, ErrIsDir) {
		t.Fatalf("Delete dir: got %v", err)
	}
	if err := lib.Delete("missing.md"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Delete missing: got %v", err)
	}
}

func TestMkdirRmdir(t *testing.T) {
	lib := newLibrary(t)
	entry, err := lib.Mkdir("a")
	if err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	if entry.Path != "a" || entry.Kind != KindDir {
		t.Fatalf("entry = %+v", entry)
	}
	if _, err := lib.Mkdir("a"); !errors.Is(err, ErrExists) {
		t.Fatalf("Mkdir existing dir: got %v", err)
	}
	writeFile(t, lib, "file.md", "x")
	if _, err := lib.Mkdir("file.md"); !errors.Is(err, ErrExists) {
		t.Fatalf("Mkdir existing file: got %v", err)
	}
	if _, err := lib.Mkdir("missing/child"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Mkdir missing parent: got %v", err)
	}
	if _, err := lib.Mkdir(""); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("Mkdir root: got %v", err)
	}

	if err := lib.Rmdir("a"); err != nil {
		t.Fatalf("Rmdir: %v", err)
	}
	if err := lib.Rmdir("a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Rmdir missing: got %v", err)
	}
	if err := lib.Rmdir("file.md"); !errors.Is(err, ErrNotDir) {
		t.Fatalf("Rmdir file: got %v", err)
	}
	if err := lib.Rmdir(""); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("Rmdir root: got %v", err)
	}

	mkdir(t, lib, "nonempty")
	writeFile(t, lib, "nonempty/a.md", "x")
	if err := lib.Rmdir("nonempty"); !errors.Is(err, ErrNotEmpty) {
		t.Fatalf("Rmdir nonempty: got %v", err)
	}
}

func TestSymlinkEscape(t *testing.T) {
	lib := newLibrary(t)
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "secret.md")
	if err := os.WriteFile(outsideFile, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(lib.Root(), "out")); err != nil {
		t.Skip("symlinks unavailable")
	}
	if err := os.Symlink(outsideFile, filepath.Join(lib.Root(), "secret.md")); err != nil {
		t.Skip("symlinks unavailable")
	}
	writeFile(t, lib, "ok.md", "ok")

	tests := []struct {
		name string
		call func() error
	}{
		{"Stat dir symlink", func() error { _, err := lib.Stat("out"); return err }},
		{"Stat file symlink", func() error { _, err := lib.Stat("secret.md"); return err }},
		{"Stat through symlink", func() error { _, err := lib.Stat("out/secret.md"); return err }},
		{"Read file symlink", func() error { _, _, err := lib.Read("secret.md"); return err }},
		{"Read through symlink", func() error { _, _, err := lib.Read("out/secret.md"); return err }},
		{"Write through symlink", func() error { _, err := lib.Write("out/new.md", []byte("x"), true); return err }},
		{"Write file symlink", func() error { _, err := lib.Write("secret.md", []byte("x"), true); return err }},
		{"List symlink", func() error { _, err := lib.List("out"); return err }},
		{"Delete file symlink", func() error { return lib.Delete("secret.md") }},
		{"Mkdir through symlink", func() error { _, err := lib.Mkdir("out/sub"); return err }},
		{"Rmdir dir symlink", func() error { return lib.Rmdir("out") }},
	}
	for _, test := range tests {
		if err := test.call(); !errors.Is(err, ErrInvalidPath) {
			t.Errorf("%s: got %v, want ErrInvalidPath", test.name, err)
		}
	}

	entries, err := lib.List("")
	if err != nil {
		t.Fatalf("List root: %v", err)
	}
	for _, entry := range entries {
		if entry.Name == "out" || entry.Name == "secret.md" {
			t.Fatalf("List included symlink %q", entry.Name)
		}
	}
	if len(entries) != 1 || entries[0].Name != "ok.md" {
		t.Fatalf("List = %+v", entries)
	}
}

func TestConcurrentWriteNoOverwrite(t *testing.T) {
	lib := newLibrary(t)
	const writers = 16
	start := make(chan struct{})
	results := make([]error, writers)
	done := make(chan struct{}, writers)
	for i := 0; i < writers; i++ {
		go func(i int) {
			<-start
			_, results[i] = lib.Write("race.md", []byte("writer-"+string(rune('a'+i))), false)
			done <- struct{}{}
		}(i)
	}
	close(start)
	for i := 0; i < writers; i++ {
		<-done
	}

	winner := -1
	for i, err := range results {
		if err == nil {
			if winner != -1 {
				t.Fatalf("multiple writers succeeded: %d and %d", winner, i)
			}
			winner = i
			continue
		}
		if !errors.Is(err, ErrExists) {
			t.Fatalf("writer %d: got %v, want ErrExists", i, err)
		}
	}
	if winner == -1 {
		t.Fatal("no writer succeeded")
	}

	data, _, err := lib.Read("race.md")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	want := "writer-" + string(rune('a'+winner))
	if string(data) != want {
		t.Fatalf("content = %q, want %q", data, want)
	}

	entries, err := os.ReadDir(lib.Root())
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".chare-tmp-") {
			t.Fatalf("leftover temp file: %q", entry.Name())
		}
	}
}
