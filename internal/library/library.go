package library

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
	"unicode"
)

type Kind string

const (
	KindDir      Kind = "dir"
	KindDocument Kind = "document"
	KindAsset    Kind = "asset"
)

type Entry struct {
	Name    string
	Path    string
	Kind    Kind
	Size    int64
	ModTime time.Time
}

var (
	ErrNotFound        = errors.New("not found")
	ErrExists          = errors.New("already exists")
	ErrInvalidPath     = errors.New("invalid path")
	ErrUnsupportedType = errors.New("unsupported file type")
	ErrNotEmpty        = errors.New("directory not empty")
	ErrIsDir           = errors.New("is a directory")
	ErrNotDir          = errors.New("not a directory")
)

type Library struct {
	root string
}

func Open(root string) (*Library, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("open library: %w", err)
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, fmt.Errorf("open library: %w", sanitize(err))
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("open library: %w", sanitize(err))
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("open library: %w", ErrNotDir)
	}
	return &Library{root: abs}, nil
}

func (l *Library) Root() string {
	return l.root
}

func (l *Library) List(rel string) ([]Entry, error) {
	cleaned, full, err := l.locate(rel)
	if err != nil {
		return nil, err
	}
	fi, err := os.Lstat(full)
	if err != nil {
		return nil, opError("list", rel, err)
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("list %q: %w", rel, ErrNotDir)
	}
	des, err := os.ReadDir(full)
	if err != nil {
		return nil, opError("list", rel, err)
	}
	entries := make([]Entry, 0, len(des))
	for _, de := range des {
		name := de.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if de.Type()&fs.ModeSymlink != 0 {
			continue
		}
		info, err := os.Lstat(filepath.Join(full, name))
		if err != nil {
			return nil, opError("list", rel, err)
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			continue
		}
		entry := entryFromInfo(path.Join(cleaned, name), info)
		if entry.Kind == "" {
			continue
		}
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		aDir, bDir := a.Kind == KindDir, b.Kind == KindDir
		if aDir != bDir {
			return aDir
		}
		an, bn := strings.ToLower(a.Name), strings.ToLower(b.Name)
		if an != bn {
			return an < bn
		}
		return a.Name < b.Name
	})
	return entries, nil
}

func (l *Library) Stat(rel string) (Entry, error) {
	cleaned, full, err := l.locate(rel)
	if err != nil {
		return Entry{}, err
	}
	fi, err := os.Lstat(full)
	if err != nil {
		return Entry{}, opError("stat", rel, err)
	}
	if cleaned == "" {
		return Entry{Path: "", Kind: KindDir}, nil
	}
	entry := entryFromInfo(cleaned, fi)
	if entry.Kind == "" {
		return Entry{}, fmt.Errorf("stat %q: %w", rel, ErrUnsupportedType)
	}
	return entry, nil
}

func (l *Library) Read(rel string) ([]byte, Entry, error) {
	cleaned, full, err := l.locate(rel)
	if err != nil {
		return nil, Entry{}, err
	}
	if cleaned == "" {
		return nil, Entry{}, fmt.Errorf("read %q: %w", rel, ErrIsDir)
	}
	fi, err := os.Lstat(full)
	if err != nil {
		return nil, Entry{}, opError("read", rel, err)
	}
	if fi.IsDir() {
		return nil, Entry{}, fmt.Errorf("read %q: %w", rel, ErrIsDir)
	}
	entry := entryFromInfo(cleaned, fi)
	if entry.Kind == "" {
		return nil, Entry{}, fmt.Errorf("read %q: %w", rel, ErrUnsupportedType)
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return nil, Entry{}, opError("read", rel, err)
	}
	return data, entry, nil
}

func (l *Library) Write(rel string, data []byte, overwrite bool) (Entry, error) {
	cleaned, full, err := l.locate(rel)
	if err != nil {
		return Entry{}, err
	}
	if cleaned == "" {
		return Entry{}, fmt.Errorf("write %q: %w", rel, ErrUnsupportedType)
	}
	parentRel := path.Dir(cleaned)
	if parentRel == "." {
		parentRel = ""
	}
	_, parentFull, err := l.locate(parentRel)
	if err != nil {
		return Entry{}, err
	}
	pfi, err := os.Lstat(parentFull)
	if err != nil {
		return Entry{}, opError("write", rel, err)
	}
	if !pfi.IsDir() {
		return Entry{}, fmt.Errorf("write %q: %w", rel, ErrNotDir)
	}

	fi, err := os.Lstat(full)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return Entry{}, opError("write", rel, err)
	}
	if err == nil && fi.IsDir() {
		return Entry{}, fmt.Errorf("write %q: %w", rel, ErrIsDir)
	}
	if KindFor(path.Base(cleaned)) == "" {
		return Entry{}, fmt.Errorf("write %q: %w", rel, ErrUnsupportedType)
	}
	if err == nil && !overwrite {
		return Entry{}, fmt.Errorf("write %q: %w", rel, ErrExists)
	}

	tmp, err := os.CreateTemp(filepath.Dir(full), ".chare-tmp-*")
	if err != nil {
		return Entry{}, opError("write", rel, err)
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			tmp.Close()
			os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(0o644); err != nil {
		return Entry{}, opError("write", rel, err)
	}
	if _, err := tmp.Write(data); err != nil {
		return Entry{}, opError("write", rel, err)
	}
	if err := tmp.Sync(); err != nil {
		return Entry{}, opError("write", rel, err)
	}
	if err := tmp.Close(); err != nil {
		return Entry{}, opError("write", rel, err)
	}
	if overwrite {
		if err := os.Rename(tmpName, full); err != nil {
			return Entry{}, opError("write", rel, err)
		}
		cleanup = false
	} else {
		if err := os.Link(tmpName, full); err != nil {
			return Entry{}, opError("write", rel, err)
		}
		if err := os.Remove(tmpName); err != nil {
			return Entry{}, opError("write", rel, err)
		}
		cleanup = false
	}

	fi, err = os.Lstat(full)
	if err != nil {
		return Entry{}, opError("write", rel, err)
	}
	return entryFromInfo(cleaned, fi), nil
}

func (l *Library) Rename(from, to string, overwrite bool) (Entry, error) {
	fromClean, fromFull, err := l.locate(from)
	if err != nil {
		return Entry{}, err
	}
	if fromClean == "" {
		return Entry{}, fmt.Errorf("rename %q: %w", from, ErrIsDir)
	}
	toClean, toFull, err := l.locate(to)
	if err != nil {
		return Entry{}, err
	}
	sfi, err := os.Lstat(fromFull)
	if err != nil {
		return Entry{}, opError("rename", from, err)
	}
	if sfi.IsDir() {
		return Entry{}, fmt.Errorf("rename %q: %w", from, ErrIsDir)
	}
	if KindFor(sfi.Name()) == "" {
		return Entry{}, fmt.Errorf("rename %q: %w", from, ErrUnsupportedType)
	}

	parentRel := path.Dir(toClean)
	if parentRel == "." {
		parentRel = ""
	}
	_, parentFull, err := l.locate(parentRel)
	if err != nil {
		return Entry{}, err
	}
	pfi, err := os.Lstat(parentFull)
	if err != nil {
		return Entry{}, opError("rename", to, err)
	}
	if !pfi.IsDir() {
		return Entry{}, fmt.Errorf("rename %q: %w", to, ErrNotDir)
	}

	dfi, err := os.Lstat(toFull)
	switch {
	case err == nil:
		if dfi.IsDir() {
			return Entry{}, fmt.Errorf("rename %q: %w", to, ErrIsDir)
		}
		if !overwrite {
			return Entry{}, fmt.Errorf("rename %q: %w", to, ErrExists)
		}
	case errors.Is(err, fs.ErrNotExist):
	default:
		return Entry{}, opError("rename", to, err)
	}
	if toClean == "" || KindFor(path.Base(toClean)) == "" {
		return Entry{}, fmt.Errorf("rename %q: %w", to, ErrUnsupportedType)
	}

	if overwrite {
		if err := os.Rename(fromFull, toFull); err != nil {
			return Entry{}, opError("rename", from, err)
		}
	} else {
		if err := os.Link(fromFull, toFull); err != nil {
			return Entry{}, opError("rename", from, err)
		}
		if err := os.Remove(fromFull); err != nil {
			return Entry{}, opError("rename", from, err)
		}
	}
	fi, err := os.Lstat(toFull)
	if err != nil {
		return Entry{}, opError("rename", to, err)
	}
	return entryFromInfo(toClean, fi), nil
}

func (l *Library) Delete(rel string) error {
	cleaned, full, err := l.locate(rel)
	if err != nil {
		return err
	}
	if cleaned == "" {
		return fmt.Errorf("delete %q: %w", rel, ErrIsDir)
	}
	fi, err := os.Lstat(full)
	if err != nil {
		return opError("delete", rel, err)
	}
	if fi.IsDir() {
		return fmt.Errorf("delete %q: %w", rel, ErrIsDir)
	}
	if KindFor(fi.Name()) == "" {
		return fmt.Errorf("delete %q: %w", rel, ErrUnsupportedType)
	}
	if err := os.Remove(full); err != nil {
		return opError("delete", rel, err)
	}
	return nil
}

func (l *Library) Mkdir(rel string) (Entry, error) {
	cleaned, full, err := l.locate(rel)
	if err != nil {
		return Entry{}, err
	}
	if cleaned == "" {
		return Entry{}, fmt.Errorf("mkdir %q: %w", rel, ErrInvalidPath)
	}
	parentRel := path.Dir(cleaned)
	if parentRel == "." {
		parentRel = ""
	}
	_, parentFull, err := l.locate(parentRel)
	if err != nil {
		return Entry{}, err
	}
	pfi, err := os.Lstat(parentFull)
	if err != nil {
		return Entry{}, opError("mkdir", rel, err)
	}
	if !pfi.IsDir() {
		return Entry{}, fmt.Errorf("mkdir %q: %w", rel, ErrNotDir)
	}
	if err := os.Mkdir(full, 0o755); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return Entry{}, fmt.Errorf("mkdir %q: %w", rel, ErrExists)
		}
		return Entry{}, opError("mkdir", rel, err)
	}
	fi, err := os.Lstat(full)
	if err != nil {
		return Entry{}, opError("mkdir", rel, err)
	}
	return entryFromInfo(cleaned, fi), nil
}

func (l *Library) Rmdir(rel string) error {
	cleaned, full, err := l.locate(rel)
	if err != nil {
		return err
	}
	if cleaned == "" {
		return fmt.Errorf("rmdir %q: %w", rel, ErrInvalidPath)
	}
	fi, err := os.Lstat(full)
	if err != nil {
		return opError("rmdir", rel, err)
	}
	if !fi.IsDir() {
		return fmt.Errorf("rmdir %q: %w", rel, ErrNotDir)
	}
	if err := os.Remove(full); err != nil {
		if errors.Is(err, syscall.ENOTEMPTY) {
			return fmt.Errorf("rmdir %q: %w", rel, ErrNotEmpty)
		}
		return opError("rmdir", rel, err)
	}
	return nil
}

func KindFor(name string) Kind {
	switch strings.ToLower(path.Ext(name)) {
	case ".md", ".markdown", ".txt":
		return KindDocument
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return KindAsset
	default:
		return ""
	}
}

func (l *Library) locate(rel string) (string, string, error) {
	cleaned, err := validateRel(rel)
	if err != nil {
		return "", "", err
	}
	if cleaned == "" {
		return "", l.root, nil
	}
	full := filepath.Join(l.root, filepath.FromSlash(cleaned))
	cur := l.root
	for _, seg := range strings.Split(cleaned, "/") {
		cur = filepath.Join(cur, seg)
		fi, err := os.Lstat(cur)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
				break
			}
			return "", "", opError("inspect", cleaned, err)
		}
		if fi.Mode()&fs.ModeSymlink != 0 {
			return "", "", fmt.Errorf("%w: %q contains a symlink", ErrInvalidPath, cleaned)
		}
	}
	return cleaned, full, nil
}

func validateRel(rel string) (string, error) {
	if rel == "" {
		return "", nil
	}
	if len(rel) > 1024 {
		return "", fmt.Errorf("%w: path is too long", ErrInvalidPath)
	}
	if path.IsAbs(rel) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("%w: absolute paths are not allowed", ErrInvalidPath)
	}
	if strings.Contains(rel, "\\") {
		return "", fmt.Errorf("%w: backslashes are not allowed", ErrInvalidPath)
	}
	if len(rel) >= 2 && rel[1] == ':' {
		c := rel[0]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
			return "", fmt.Errorf("%w: windows volume names are not allowed", ErrInvalidPath)
		}
	}
	for _, r := range rel {
		if r == 0 || unicode.IsControl(r) {
			return "", fmt.Errorf("%w: control characters are not allowed", ErrInvalidPath)
		}
	}
	for _, seg := range strings.Split(rel, "/") {
		if seg == "" {
			return "", fmt.Errorf("%w: empty path segment", ErrInvalidPath)
		}
		if seg == "." || seg == ".." {
			return "", fmt.Errorf("%w: relative path segment %q", ErrInvalidPath, seg)
		}
		if strings.HasPrefix(seg, ".") {
			return "", fmt.Errorf("%w: hidden path segment %q", ErrInvalidPath, seg)
		}
	}
	return path.Clean(rel), nil
}

func entryFromInfo(rel string, fi fs.FileInfo) Entry {
	entry := Entry{
		Name:    fi.Name(),
		Path:    rel,
		ModTime: fi.ModTime(),
	}
	if fi.IsDir() {
		entry.Kind = KindDir
		return entry
	}
	entry.Kind = KindFor(fi.Name())
	entry.Size = fi.Size()
	return entry
}

func opError(op, rel string, err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("%s %q: %w", op, rel, ErrNotFound)
	case errors.Is(err, fs.ErrExist):
		return fmt.Errorf("%s %q: %w", op, rel, ErrExists)
	case errors.Is(err, syscall.ENOTDIR):
		return fmt.Errorf("%s %q: %w", op, rel, ErrNotDir)
	case errors.Is(err, syscall.EISDIR):
		return fmt.Errorf("%s %q: %w", op, rel, ErrIsDir)
	}
	return fmt.Errorf("%s %q: %w", op, rel, sanitize(err))
}

func sanitize(err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return sanitize(pe.Err)
	}
	var le *os.LinkError
	if errors.As(err, &le) {
		return sanitize(le.Err)
	}
	return err
}
