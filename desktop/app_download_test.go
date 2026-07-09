package main

import (
	"net/http"
	"testing"
)

func TestParseDispositionFilename(t *testing.T) {
	cases := []struct {
		cd   string
		want string
	}{
		{`attachment; filename="report.pdf"`, "report.pdf"},
		{`inline; filename=hello.txt`, "hello.txt"},
		{`attachment; filename*=UTF-8''%E5%9B%BE%E5%83%8F.png`, "图像.png"},
		{`attachment; filename="fallback.txt"; filename*=UTF-8''real.png`, "real.png"},
		{`inline`, ""},
	}
	for _, c := range cases {
		if got := parseDispositionFilename(c.cd); got != c.want {
			t.Errorf("parseDispositionFilename(%q) = %q, want %q", c.cd, got, c.want)
		}
	}
}

func TestSanitizeDownloadName(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"normal.bin", "normal.bin"},
		{"../escape.bin", "escape.bin"},      // path sep stripped by filepath.Base + map
		{"a/b/c.bin", "c.bin"},               // filepath.Base strips dir
		{`bad"name"*?.bin`, "bad_name___.bin"},   // quotes/stars/question replaced with _
		{"weird:name?.bin", "weird_name_.bin"}, // colon/question replaced with _
	}
	for _, c := range cases {
		if got := sanitizeDownloadName(c.in); got != c.want {
			t.Errorf("sanitizeDownloadName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestDetectDownloadFilename(t *testing.T) {
	// Content-Disposition wins.
	resp := &http.Response{Header: http.Header{"Content-Disposition": []string{`attachment; filename="doc.pdf"`}}}
	if got := detectDownloadFilename(resp, "http://127.0.0.1:8080/mem/ignore"); got != "doc.pdf" {
		t.Errorf("got %q want doc.pdf", got)
	}

	// Fallback to URL path basename.
	resp2 := &http.Response{Header: http.Header{}}
	if got := detectDownloadFilename(resp2, "http://127.0.0.1:8080/mem/memabc123/image.png"); got != "image.png" {
		t.Errorf("got %q want image.png", got)
	}

	// Last resort.
	if got := detectDownloadFilename(&http.Response{Header: http.Header{}}, "http://127.0.0.1:8080"); got != "download.bin" {
		t.Errorf("got %q want download.bin", got)
	}
}
