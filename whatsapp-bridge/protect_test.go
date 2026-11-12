package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// The real routes behind protect: every case below is answered before the handlers need WhatsApp or the store.
func protectedAPI(token string) http.Handler {
	return protect(newRESTHandler(nil, nil), token, 8741)
}

func TestProtect(t *testing.T) {
	const j = "application/json"
	cases := []struct {
		name, method, target, host, token string
		contentType                       *string
		body                              string
		want                              int
		wantBody                          string
	}{
		{"probe without token", "POST", "/api/send", "127.0.0.1:8741", "", ptr(j), "{}", 401, "Unauthorized\n"},
		{"probe with token and JSON", "POST", "/api/send", "127.0.0.1:8741", testToken, ptr(j), "{}", 400, "Recipient is required\n"},
		{"download probe with token and JSON", "POST", "/api/download", "127.0.0.1:8741", testToken, ptr(j), "{}", 400, "Message ID and Chat JID are required\n"},
		{"localhost host", "POST", "/api/send", "localhost:8741", testToken, ptr(j), "{}", 400, "Recipient is required\n"},
		{"host in upper case", "POST", "/api/send", "LOCALHOST:8741", testToken, ptr(j), "{}", 400, "Recipient is required\n"},

		{"wrong host with token", "POST", "/api/send", "evil.example:8741", testToken, ptr(j), "{}", 403, "Forbidden host\n"},
		{"wrong host without token", "POST", "/api/send", "evil.example", "", ptr(j), "{}", 403, "Forbidden host\n"},
		{"wrong port in host", "POST", "/api/send", "127.0.0.1:8080", testToken, ptr(j), "{}", 403, "Forbidden host\n"},
		{"host without port", "POST", "/api/send", "127.0.0.1", testToken, ptr(j), "{}", 403, "Forbidden host\n"},
		{"host with trailing dot", "POST", "/api/send", "localhost.:8741", testToken, ptr(j), "{}", 403, "Forbidden host\n"},
		{"IPv6 loopback host", "POST", "/api/send", "[::1]:8741", testToken, ptr(j), "{}", 403, "Forbidden host\n"},
		{"wrong host on another path", "GET", "/", "evil.example:8741", "", nil, "", 403, "Forbidden host\n"},

		{"wrong token of the same length", "POST", "/api/send", "127.0.0.1:8741", strings.Repeat("f", 64), ptr(j), "{}", 401, "Unauthorized\n"},
		{"wrong token, shorter", "POST", "/api/send", "127.0.0.1:8741", testToken[:10], ptr(j), "{}", 401, "Unauthorized\n"},
		{"wrong token, longer", "POST", "/api/send", "127.0.0.1:8741", testToken + "0", ptr(j), "{}", 401, "Unauthorized\n"},
		{"token in the query string only", "POST", "/api/send?X-Bridge-Token=" + testToken + "&token=" + testToken, "127.0.0.1:8741", "", ptr(j), "{}", 401, "Unauthorized\n"},
		{"no token and text/plain: token first", "POST", "/api/send", "127.0.0.1:8741", "", ptr("text/plain"), "{}", 401, "Unauthorized\n"},

		{"text/plain", "POST", "/api/send", "127.0.0.1:8741", testToken, ptr("text/plain"), "{}", 415, ""},
		{"form", "POST", "/api/send", "127.0.0.1:8741", testToken, ptr("application/x-www-form-urlencoded"), "{}", 415, ""},
		{"no content type", "POST", "/api/send", "127.0.0.1:8741", testToken, nil, "{}", 415, ""},
		{"JSON look-alike", "POST", "/api/send", "127.0.0.1:8741", testToken, ptr("application/jsonx"), "{}", 415, ""},
		{"JSON with charset", "POST", "/api/send", "127.0.0.1:8741", testToken, ptr("application/json; charset=utf-8"), "{}", 400, "Recipient is required\n"},
		{"JSON in mixed case", "POST", "/api/send", "127.0.0.1:8741", testToken, ptr("Application/JSON"), "{}", 400, "Recipient is required\n"},
		{"GET with JSON", "GET", "/api/send", "127.0.0.1:8741", testToken, ptr(j), "", 405, ""},

		{"OPTIONS preflight without token", "OPTIONS", "/api/send", "127.0.0.1:8741", "", nil, "", 401, "Unauthorized\n"},
		{"unknown path without token", "POST", "/nope", "127.0.0.1:8741", "", ptr(j), "{}", 401, "Unauthorized\n"},
		{"unknown path with token", "POST", "/nope", "127.0.0.1:8741", testToken, ptr(j), "{}", 404, ""},
	}
	handler := protectedAPI(testToken)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, tc.target, strings.NewReader(tc.body))
			r.Host = tc.host
			if tc.token != "" {
				r.Header.Set("X-Bridge-Token", tc.token)
			}
			if tc.contentType != nil {
				r.Header.Set("Content-Type", *tc.contentType)
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != tc.want || (tc.wantBody != "" && w.Body.String() != tc.wantBody) {
				t.Errorf("got %d %q, want %d %q", w.Code, w.Body.String(), tc.want, tc.wantBody)
			}
			if origin := w.Header().Get("Access-Control-Allow-Origin"); origin != "" {
				t.Errorf("CORS header set: %q", origin)
			}
		})
	}
}

// With WHATSAPP_BRIDGE_TOKEN unset there is no token check: host and content type still are. Tresso sends its probe
// without a token and reads the 400 as a bridge without protection.
func TestProtectWithoutToken(t *testing.T) {
	handler := protectedAPI("")

	r := httptest.NewRequest(http.MethodPost, "/api/send", strings.NewReader("{}"))
	r.Host = "127.0.0.1:8741"
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest || w.Body.String() != "Recipient is required\n" {
		t.Errorf("probe without token: got %d %q, want 400 Recipient is required", w.Code, w.Body.String())
	}

	r = httptest.NewRequest(http.MethodPost, "/api/send", strings.NewReader("{}"))
	r.Host = "evil.example:8741"
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("wrong host without token: got %d, want 403", w.Code)
	}

	r = httptest.NewRequest(http.MethodPost, "/api/send", strings.NewReader("{}"))
	r.Host = "127.0.0.1:8741"
	r.Header.Set("Content-Type", "text/plain")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusUnsupportedMediaType {
		t.Errorf("text/plain without token: got %d, want 415", w.Code)
	}
}

func ptr(s string) *string { return &s }
