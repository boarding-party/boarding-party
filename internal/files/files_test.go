package files

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func put(t *testing.T, s *Store, scope, name, body string) Info {
	t.Helper()
	in, err := s.Put(scope, name, strings.NewReader(body))
	if err != nil {
		t.Fatalf("put %s %s: %v", scope, name, err)
	}
	return in
}

func TestNameRule(t *testing.T) {
	s := New(t.TempDir())
	for _, n := range []string{"logo.png", "a", "data-1_v2.csv", "0abc", strings.Repeat("a", 64)} {
		if _, err := s.Put(ScopeProject, n, strings.NewReader("x")); err != nil {
			t.Errorf("%q should be valid: %v", n, err)
		}
	}
	for _, n := range []string{"", ".hidden", "-x", "_x", "Logo.png", "a b", "a/b", "..", ".", "a\\b", strings.Repeat("a", 65), ".upload-1"} {
		if _, err := s.Put(ScopeProject, n, strings.NewReader("x")); !errors.Is(err, ErrBadName) {
			t.Errorf("%q: err = %v, want ErrBadName", n, err)
		}
	}
}

func TestScopes(t *testing.T) {
	s := New(t.TempDir())
	for _, sc := range []string{"", "persona:", "persona", "other", "project:x", "persona:" + strings.Repeat("a", 101)} {
		if _, err := s.Put(sc, "a.txt", strings.NewReader("x")); !errors.Is(err, ErrBadScope) {
			t.Errorf("scope %q: err = %v", sc, err)
		}
	}
	// A persona name that looks like a path stays inside the root.
	in := put(t, s, "persona:../../etc", "a.txt", "x")
	if in.Scope != "persona:../../etc" {
		t.Errorf("scope = %q", in.Scope)
	}
}

func TestLimits(t *testing.T) {
	s := New(t.TempDir())
	s.MaxFile, s.MaxTotal = 10, 15
	if _, err := s.Put(ScopeProject, "big.bin", strings.NewReader(strings.Repeat("x", 11))); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("per file: %v", err)
	}
	put(t, s, ScopeProject, "a.bin", strings.Repeat("x", 10))
	if _, err := s.Put(ScopeProject, "b.bin", strings.NewReader(strings.Repeat("x", 6))); !errors.Is(err, ErrTotalTooLarge) {
		t.Fatalf("total: %v", err)
	}
	// Replacing a file does not count its old size twice.
	put(t, s, ScopeProject, "a.bin", strings.Repeat("y", 10))
	put(t, s, "persona:Ann", "b.bin", strings.Repeat("x", 5))
	if l, _ := s.List(); len(l) != 2 {
		t.Fatalf("list after rejects = %+v", l)
	}
	// A rejected upload leaves no temp file or half-written replacement.
	if _, err := s.Put(ScopeProject, "a.bin", strings.NewReader(strings.Repeat("z", 11))); !errors.Is(err, ErrTooLarge) {
		t.Fatal(err)
	}
	f, _, err := s.OpenFor("Bob", "a.bin")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if b, _ := io.ReadAll(f); string(b) != strings.Repeat("y", 10) {
		t.Fatalf("content = %q", b)
	}
}

func TestInfoReplaceAndDelete(t *testing.T) {
	s := New(t.TempDir())
	in := put(t, s, ScopeProject, "hello.txt", "hello world")
	if in.Size != 11 || !strings.HasPrefix(in.ContentType, "text/plain") || in.Uploaded.IsZero() || in.Scope != ScopeProject {
		t.Fatalf("info = %+v", in)
	}
	png := "\x89PNG\r\n\x1a\n" + strings.Repeat("\x00", 20)
	if in := put(t, s, ScopeProject, "hello.txt", png); in.ContentType != "image/png" || in.Size != int64(len(png)) {
		t.Fatalf("replaced = %+v", in)
	}
	if l, _ := s.List(); len(l) != 1 {
		t.Fatalf("list = %+v", l)
	}
	if err := s.Delete(ScopeProject, "hello.txt"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ScopeProject, "hello.txt"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
	if err := s.Delete(ScopeProject, "../x"); !errors.Is(err, ErrBadName) {
		t.Fatalf("bad name delete: %v", err)
	}
}

func TestPersonaPrecedenceAndVisibility(t *testing.T) {
	s := New(t.TempDir())
	put(t, s, ScopeProject, "logo.png", "project-logo")
	put(t, s, ScopeProject, "shared.csv", "shared")
	put(t, s, "persona:Ann", "logo.png", "ann-logo")
	put(t, s, "persona:Ann", "ann-only.pdf", "ann")
	put(t, s, "persona:Bob", "bob-only.pdf", "bob")

	names := func(persona string) string {
		l, err := s.ForPersona(persona)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, f := range l {
			out = append(out, f.Name+"="+f.Scope)
		}
		return strings.Join(out, ",")
	}
	if got, want := names("Ann"), "ann-only.pdf=persona:Ann,logo.png=persona:Ann,shared.csv=project"; got != want {
		t.Errorf("Ann: %s", got)
	}
	if got, want := names("Bob"), "bob-only.pdf=persona:Bob,logo.png=project,shared.csv=project"; got != want {
		t.Errorf("Bob: %s", got)
	}
	read := func(persona, name string) (string, error) {
		f, _, err := s.OpenFor(persona, name)
		if err != nil {
			return "", err
		}
		defer f.Close()
		b, _ := io.ReadAll(f)
		return string(b), nil
	}
	if b, _ := read("Ann", "logo.png"); b != "ann-logo" {
		t.Errorf("Ann logo = %q", b)
	}
	if b, _ := read("Bob", "logo.png"); b != "project-logo" {
		t.Errorf("Bob logo = %q", b)
	}
	if _, err := read("Bob", "ann-only.pdf"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Bob reading Ann's file: %v", err)
	}
	// Deleting the persona's copy uncovers the project file again.
	if err := s.Delete("persona:Ann", "logo.png"); err != nil {
		t.Fatal(err)
	}
	if b, _ := read("Ann", "logo.png"); b != "project-logo" {
		t.Errorf("after delete = %q", b)
	}
}

func TestListNotBlockedBySlowPut(t *testing.T) {
	s := New(t.TempDir())
	put(t, s, ScopeProject, "a.txt", "x")
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() { _, err := s.Put(ScopeProject, "slow.txt", pr); done <- err }()
	pw.Write([]byte("partial")) // the Put is now blocked reading the body
	listed := make(chan int, 1)
	go func() { l, _ := s.List(); listed <- len(l) }()
	select {
	case n := <-listed:
		if n != 1 {
			t.Errorf("list = %d files, want 1 (slow upload not visible yet)", n)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("List blocked behind a slow Put")
	}
	pw.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if l, _ := s.List(); len(l) != 2 {
		t.Fatalf("after put: %+v", l)
	}
}

func TestNewRemovesLeftoverTempFiles(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "project"), 0o755)
	tmp := filepath.Join(root, "project", tmpPrefix+"123")
	os.WriteFile(tmp, []byte("half"), 0o644)
	os.WriteFile(filepath.Join(root, "project", "keep.txt"), []byte("k"), 0o644)
	s := New(root)
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Errorf("temp file survived: %v", err)
	}
	if l, _ := s.List(); len(l) != 1 || l[0].Name != "keep.txt" {
		t.Errorf("list = %+v", l)
	}
}
