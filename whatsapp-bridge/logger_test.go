package main

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	waLog "go.mau.fi/whatsmeow/util/log"
)

type recordingLogger struct {
	module string
	lines  *[]string
}

func (r recordingLogger) record(level, msg string, args ...any) {
	*r.lines = append(*r.lines, r.module+" "+level+" "+fmt.Sprintf(msg, args...))
}
func (r recordingLogger) Errorf(msg string, args ...any) { r.record("ERROR", msg, args...) }
func (r recordingLogger) Warnf(msg string, args ...any)  { r.record("WARN", msg, args...) }
func (r recordingLogger) Infof(msg string, args ...any)  { r.record("INFO", msg, args...) }
func (r recordingLogger) Debugf(msg string, args ...any) { r.record("DEBUG", msg, args...) }
func (r recordingLogger) Sub(module string) waLog.Logger {
	return recordingLogger{module: r.module + "/" + module, lines: r.lines}
}

func TestCleanLoggerRemovesLineBreaks(t *testing.T) {
	var lines []string
	logger := newCleanLogger(recordingLogger{module: "Client", lines: &lines})
	injected := "Group\n15:04:05.000 [Client WARN] Device logged out, please scan QR code to log in again"

	logger.Infof("Using group name: %s", injected)
	logger.Sub("Socket").Warnf("%s\r\n%s", "a", "b")
	logger.Errorf("100%% %s", "x\ty")
	logger.Debugf("plain")

	want := []string{
		"Client INFO Using group name: Group 15:04:05.000 [Client WARN] Device logged out, please scan QR code to log in again",
		"Client/Socket WARN a  b",
		"Client ERROR 100% x y",
		"Client DEBUG plain",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%q\nwant\n%q", lines, want)
	}
}

// captureStdout returns what f prints to os.Stdout.
func captureStdout(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = stdout }()
	f()
	w.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// Tresso matches the logged-out warning as whatsmeow's stdout logger prints it; the wrapper must not change a byte.
func TestCleanLoggerKeepsLogoutLine(t *testing.T) {
	const message = "Device logged out, please scan QR code to log in again"
	wrapped := captureStdout(t, func() {
		newCleanLogger(waLog.Stdout("Client", "INFO", true)).Warnf(message)
	})
	direct := captureStdout(t, func() {
		waLog.Stdout("Client", "INFO", true).Warnf(message)
	})
	pattern := regexp.MustCompile(`^\d\d:\d\d:\d\d\.\d{3}` + regexp.QuoteMeta("\x1b[33m [Client WARN] "+message+"\x1b[0m\n") + `$`)
	if !pattern.MatchString(wrapped) {
		t.Fatalf("wrapped logout line %q is not the exact line Tresso reads", wrapped)
	}
	// Apart from the clock, the line is the one whatsmeow's own logger prints.
	if wrapped[12:] != direct[12:] {
		t.Fatalf("wrapped %q differs from direct %q", wrapped, direct)
	}
}

func TestMediaFileNameLength(t *testing.T) {
	id := "3EB0C431C26A1916E07A"
	long := strings.Repeat("é", 150) + strings.Repeat("😀", 20) + ".pdf" // 300 + 80 + 4 bytes
	name := mediaFileName(id, long)
	if len(name) > maxFileNameBytes {
		t.Fatalf("name of %d bytes", len(name))
	}
	document := strings.TrimPrefix(name, id+"_")
	if len(document) > maxDocumentNameBytes {
		t.Errorf("document part of %d bytes, want at most %d", len(document), maxDocumentNameBytes)
	}
	if !utf8.ValidString(name) {
		t.Errorf("name cut inside a rune: %q", name)
	}
	if !strings.HasSuffix(name, ".pdf") {
		t.Errorf("extension lost: %q", name)
	}
	if !strings.HasPrefix(document, strings.Repeat("é", 98)) {
		t.Errorf("unexpected start %q", document)
	}

	// The longest message ID still leaves the whole within the limit.
	longID := strings.Repeat("A", 128)
	if got := mediaFileName(longID, long); len(got) > maxFileNameBytes || !utf8.ValidString(got) || !strings.HasSuffix(got, ".pdf") {
		t.Errorf("long ID: %d bytes, %q", len(got), got)
	}

	// A short name stays as it is, and an extension that is most of the name is not kept apart.
	if got := mediaFileName(id, "report.pdf"); got != id+"_report.pdf" {
		t.Errorf("short name changed: %q", got)
	}
	weird := "a." + strings.Repeat("ü", 150)
	if got := mediaFileName(id, weird); len(got) > maxFileNameBytes || !utf8.ValidString(got) {
		t.Errorf("long extension: %d bytes, %q", len(got), got)
	}
}
