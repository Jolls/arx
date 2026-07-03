package urlutil

import (
	"reflect"
	"testing"
)

func TestIsHTTPURL(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{"https://example.com", true},
		{"http://example.com", true},
		{"LOCAL:foo/bar.pdf", false},
		{"ftp://example.com", false},
		{"", false},
	}
	for _, c := range cases {
		if got := IsHTTPURL(c.input); got != c.want {
			t.Errorf("IsHTTPURL(%q) = %v, want %v", c.input, got, c.want)
		}
	}
}

func TestIsLocalFile(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{"LOCAL:foo/bar.pdf", true},
		{"local:foo/bar.pdf", true},  // case-insensitive
		{"LOCAL:foo\\bar\\", true},
		{"https://example.com", false},
		{"", false},
	}
	for _, c := range cases {
		if got := IsLocalFile(c.input); got != c.want {
			t.Errorf("IsLocalFile(%q) = %v, want %v", c.input, got, c.want)
		}
	}
}

func TestIsLocalDir(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{"LOCAL:foo/bar/", true},
		{"LOCAL:foo\\bar\\", true},
		{"LOCAL:foo/bar.pdf", false},  // no trailing slash
		{"https://example.com/", false},
		{"", false},
	}
	for _, c := range cases {
		if got := IsLocalDir(c.input); got != c.want {
			t.Errorf("IsLocalDir(%q) = %v, want %v", c.input, got, c.want)
		}
	}
}

func TestLocalFileURL(t *testing.T) {
	cases := []struct {
		val, prefix, want string
	}{
		{"LOCAL:foo\\bar.pdf", "/local/", "/local/foo/bar.pdf"},
		{"LOCAL:foo/bar.pdf", "/local/", "/local/foo/bar.pdf"},
		{"LOCAL:bar.pdf", "/local/", "/local/bar.pdf"},
		{"LOCAL:/bar.pdf", "/local/", "/local/bar.pdf"},  // leading slash stripped
	}
	for _, c := range cases {
		if got := LocalFileURL(c.val, c.prefix); got != c.want {
			t.Errorf("LocalFileURL(%q, %q) = %q, want %q", c.val, c.prefix, got, c.want)
		}
	}
}

func TestLocalDirURL(t *testing.T) {
	cases := []struct {
		val, prefix, want string
	}{
		{"LOCAL:foo\\", "/local-dir/", "/local-dir/foo"},
		{"LOCAL:foo/", "/local-dir/", "/local-dir/foo"},
		{"LOCAL:foo/bar/", "/local-dir/", "/local-dir/foo/bar"},
	}
	for _, c := range cases {
		if got := LocalDirURL(c.val, c.prefix); got != c.want {
			t.Errorf("LocalDirURL(%q, %q) = %q, want %q", c.val, c.prefix, got, c.want)
		}
	}
}

func TestFileBaseName(t *testing.T) {
	cases := []struct {
		input, want string
	}{
		{"foo\\bar\\baz.pdf", "baz.pdf"},
		{"foo/bar/baz.pdf", "baz.pdf"},
		{"baz.pdf", "baz.pdf"},
	}
	for _, c := range cases {
		if got := FileBaseName(c.input); got != c.want {
			t.Errorf("FileBaseName(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestFileIcon(t *testing.T) {
	cases := []struct {
		input, want string
	}{
		{"doc.pdf", "bi-file-pdf text-danger"},
		{"DOC.PDF", "bi-file-pdf text-danger"},  // case-insensitive
		{"report.doc", "bi-file-word text-primary"},
		{"report.docx", "bi-file-word text-primary"},
		{"data.xls", "bi-file-excel text-success"},
		{"data.xlsx", "bi-file-excel text-success"},
		{"readme.txt", "bi-file-earmark"},
		{"noextension", "bi-file-earmark"},
	}
	for _, c := range cases {
		if got := FileIcon(c.input); got != c.want {
			t.Errorf("FileIcon(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestIsPDF(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{"doc.pdf", true},
		{"DOC.PDF", true},
		{"doc.docx", false},
		{"", false},
	}
	for _, c := range cases {
		if got := IsPDF(c.input); got != c.want {
			t.Errorf("IsPDF(%q) = %v, want %v", c.input, got, c.want)
		}
	}
}

func TestIsImage(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{"photo.png", true},
		{"PHOTO.PNG", true},
		{"photo.jpg", true},
		{"photo.jpeg", true},
		{"photo.gif", true},
		{"photo.webp", true},
		{"doc.pdf", false},
		{"", false},
	}
	for _, c := range cases {
		if got := IsImage(c.input); got != c.want {
			t.Errorf("IsImage(%q) = %v, want %v", c.input, got, c.want)
		}
	}
}

func TestSafePathSegments(t *testing.T) {
	cases := []struct {
		input string
		want  []string
	}{
		{"foo/bar/baz.pdf", []string{"foo", "bar", "baz.pdf"}},
		{"foo//bar", []string{"foo", "bar"}},         // empty segments dropped
		{"../../../etc/passwd", []string{"etc", "passwd"}},  // traversal blocked — .. dropped, remaining segments kept
		{"foo/../bar", []string{"foo", "bar"}},
		{"", []string{}},
	}
	for _, c := range cases {
		got := SafePathSegments(c.input)
		if len(got) == 0 && len(c.want) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("SafePathSegments(%q) = %v, want %v", c.input, got, c.want)
		}
	}
}
