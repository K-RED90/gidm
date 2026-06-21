package httpx

import "testing"

func TestSanitizeFilename(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "report.pdf", "report.pdf"},
		{"unix traversal", "../../etc/passwd", "passwd"},
		{"windows traversal", `..\..\windows\system32\cmd.exe`, "cmd.exe"},
		{"absolute unix path", "/etc/passwd", "passwd"},
		{"mixed separators", `a/b\c/d.txt`, "d.txt"},
		{"dotdot only", "..", ""},
		{"dot only", ".", ""},
		{"empty", "", ""},
		{"hidden file preserved", ".bashrc", ".bashrc"},
		{"trailing dot stripped", "evil.exe.", "evil.exe"},
		{"trailing dots and spaces", "x. . ", "x"},
		{"control bytes removed", "a\x00b\tc.txt", "abc.txt"},
		{"surrounding space trimmed", "  spacey.txt  ", "spacey.txt"},
	}
	for _, tc := range cases {
		if got := SanitizeFilename(tc.in); got != tc.want {
			t.Errorf("%s: SanitizeFilename(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}
