package main

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClean(t *testing.T) {
	cases := map[string]string{
		"plain text": "plain text",
		"line\nStarting REST API server on 127.0.0.1:1...": "line Starting REST API server on 127.0.0.1:1...",
		"cr\rreturn":                 "cr return",
		"tab\tand\x00nul\x1b[31m":    "tab and nul [31m",
		"unicode line para":          "unicode line para",
		"next\u0085line":             "next line",
		"Zoë ✓ 😀":                    "Zoë ✓ 😀",
		"31612345678@s.whatsapp.net": "31612345678@s.whatsapp.net",
		"\n✓ Connected to WhatsApp!": " ✓ Connected to WhatsApp!",
		"\r\n":                       "  ",
		"":                           "",
	}
	for in, want := range cases {
		if got := clean(in); got != want {
			t.Errorf("clean(%q) = %q, want %q", in, got, want)
		}
		if strings.ContainsAny(clean(in), "\n\r") {
			t.Errorf("clean(%q) still holds a line break", in)
		}
	}
}

func TestValidChatJID(t *testing.T) {
	for _, jid := range []string{
		"31612345678@s.whatsapp.net",
		"120363012345678901@g.us",
		"123456789012345@lid",
		"120363012345678901@newsletter",
	} {
		if err := validChatJID(jid); err != nil {
			t.Errorf("validChatJID(%q) = %v, want nil", jid, err)
		}
	}
	for _, jid := range []string{
		"",
		".",
		"..",
		"@s.whatsapp.net",
		"31612345678",
		"31612345678@example.com",
		"31612345678@broadcast",
		"../x@s.whatsapp.net",
		"a/b@s.whatsapp.net",
		"a\\b@s.whatsapp.net",
		"..@s.whatsapp.net",
		"x@g.us@/../../etc",
		"x@g.us/..",
		"x\x00@g.us",
	} {
		if err := validChatJID(jid); err == nil {
			t.Errorf("validChatJID(%q) accepted", jid)
		}
	}
}

func TestValidMessageID(t *testing.T) {
	for _, id := range []string{"3EB0C431C26A1916E07A", "ABCDEF0123456789", "a-b_c"} {
		if !validMessageID(id) {
			t.Errorf("validMessageID(%q) = false", id)
		}
	}
	for _, id := range []string{"", ".", "..", "a/b", "a\\b", "a.b", "a b", "a\nb", strings.Repeat("A", 129)} {
		if validMessageID(id) {
			t.Errorf("validMessageID(%q) = true", id)
		}
	}
}

func TestMediaFileNamesDoNotCollide(t *testing.T) {
	first := mediaFileName("3EB0AAAA", "report.pdf")
	second := mediaFileName("3EB0BBBB", "report.pdf")
	if first == second {
		t.Fatalf("two messages with one file name share %q", first)
	}
	if first != "3EB0AAAA_report.pdf" {
		t.Errorf("unexpected name %q", first)
	}
}

func TestStoredFileMatchesAndWritePrivateFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, mediaFileName("3EB0AAAA", "report.pdf"))
	data := []byte("the first report")
	sum := sha256.Sum256(data)

	if storedFileMatches(path, sum[:]) {
		t.Fatal("a missing file matched")
	}
	if err := writePrivateFile(path, data); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0600 {
		t.Errorf("file mode %o, want 600", mode)
	}
	if !storedFileMatches(path, sum[:]) {
		t.Error("the file that was written does not match its hash")
	}
	other := sha256.Sum256([]byte("the second report"))
	if storedFileMatches(path, other[:]) {
		t.Error("a file matched another message's hash")
	}
	if storedFileMatches(path, nil) {
		t.Error("a file matched without a stored hash")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("temporary files left behind: %v", entries)
	}
}

func TestPrepareStore(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "store")

	// A new store.
	if err := prepareStore(dir); err != nil {
		t.Fatal(err)
	}
	assertMode(t, dir, 0700)
	assertMode(t, filepath.Join(dir, "whatsapp.db"), 0600)
	assertMode(t, filepath.Join(dir, "messages.db"), 0600)

	// An existing store that others could read.
	chat := filepath.Join(dir, "31612345678@s.whatsapp.net")
	if err := os.Mkdir(chat, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"whatsapp.db", "messages.db", "messages.db-wal"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(filepath.Join(dir, name), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := prepareStore(dir); err != nil {
		t.Fatal(err)
	}
	assertMode(t, dir, 0700)
	assertMode(t, chat, 0700)
	assertMode(t, filepath.Join(dir, "whatsapp.db"), 0600)
	assertMode(t, filepath.Join(dir, "messages.db"), 0600)
	assertMode(t, filepath.Join(dir, "messages.db-wal"), 0600)
	if data, _ := os.ReadFile(filepath.Join(dir, "messages.db")); string(data) != "x" {
		t.Error("prepareStore changed an existing database")
	}
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Errorf("%s: mode %o, want %o", filepath.Base(path), got, want)
	}
}
