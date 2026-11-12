package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

func TestParseBridgePort(t *testing.T) {
	valid := map[string]int{"": 8741, "1": 1, "8741": 8741, "9000": 9000, "65535": 65535}
	for value, want := range valid {
		got, err := parseBridgePort(value)
		if err != nil || got != want {
			t.Errorf("parseBridgePort(%q) = %d, %v; want %d", value, got, err, want)
		}
	}
	for _, value := range []string{"0", "65536", "-1", "abc", "80a", " 8741", "8741 ", "1e3", "99999999999999999999"} {
		if got, err := parseBridgePort(value); err == nil {
			t.Errorf("parseBridgePort(%q) = %d, want an error", value, got)
		}
	}
}

func TestMediaRetryWaiters(t *testing.T) {
	const id = types.MessageID("3EB0TESTWAITER")
	t.Cleanup(func() {
		mediaRetryMu.Lock()
		delete(mediaRetryWaiters, id)
		mediaRetryMu.Unlock()
	})

	first, err := registerMediaRetryWaiter(id)
	if err != nil {
		t.Fatalf("first register: %v", err)
	}

	// A second request for the same message is refused and leaves the first waiter in place.
	if second, err := registerMediaRetryWaiter(id); !errors.Is(err, errMediaRetryPending) || second != nil {
		t.Fatalf("second register = %v, %v; want nil, errMediaRetryPending", second, err)
	}
	if errMediaRetryPending.Error() != "a re-upload of this message is already being requested" {
		t.Fatalf("unexpected message %q", errMediaRetryPending.Error())
	}

	// A release by someone else's (stale) waiter leaves the current one alone.
	stale := make(chan *events.MediaRetry, 1)
	releaseMediaRetryWaiter(id, stale)
	handleMediaRetry(&events.MediaRetry{MessageID: id})
	select {
	case <-first:
	default:
		t.Fatal("the answer did not reach the registered waiter after a stale release")
	}

	// The owner's release removes it, and the id can be requested again.
	releaseMediaRetryWaiter(id, first)
	mediaRetryMu.Lock()
	_, still := mediaRetryWaiters[id]
	mediaRetryMu.Unlock()
	if still {
		t.Fatal("waiter still registered after its own release")
	}
	again, err := registerMediaRetryWaiter(id)
	if err != nil {
		t.Fatalf("register after release: %v", err)
	}
	releaseMediaRetryWaiter(id, again)

	// An answer for a message nobody waits for is dropped without blocking.
	handleMediaRetry(&events.MediaRetry{MessageID: id})
}

type fakeLIDs struct {
	lidToPN map[string]string
	pnToLID map[string]string
	err     error
}

func (f fakeLIDs) GetPNForLID(_ context.Context, lid types.JID) (types.JID, error) {
	if f.err != nil {
		return types.JID{}, f.err
	}
	if pn, ok := f.lidToPN[lid.User]; ok {
		return types.NewJID(pn, types.DefaultUserServer), nil
	}
	return types.JID{}, nil
}

func (f fakeLIDs) GetLIDForPN(_ context.Context, pn types.JID) (types.JID, error) {
	if f.err != nil {
		return types.JID{}, f.err
	}
	if lid, ok := f.pnToLID[pn.User]; ok {
		return types.NewJID(lid, types.HiddenUserServer), nil
	}
	return types.JID{}, nil
}

func TestSenderJID(t *testing.T) {
	store := fakeLIDs{
		// A 15 digit phone number and a short LID: the length rule would get both wrong.
		lidToPN: map[string]string{"12345678": "123456789012345"},
		pnToLID: map[string]string{"123456789012345": "12345678"},
	}
	broken := fakeLIDs{err: errors.New("store closed")}
	cases := []struct {
		name   string
		lids   lidLookup
		sender string
		want   string
	}{
		{"full JID kept", store, "31612345678@s.whatsapp.net", "31612345678@s.whatsapp.net"},
		{"full LID kept", store, "98765432109876543@lid", "98765432109876543@lid"},
		{"store knows it as a LID", store, "12345678", "12345678@lid"},
		{"store knows it as a phone number", store, "123456789012345", "123456789012345@s.whatsapp.net"},
		{"unknown short id falls back to phone", store, "31612345678", "31612345678@s.whatsapp.net"},
		{"unknown long id falls back to LID", store, "98765432109876543", "98765432109876543@lid"},
		{"store error falls back to length", broken, "12345678", "12345678@s.whatsapp.net"},
		{"no store falls back to length", nil, "98765432109876543", "98765432109876543@lid"},
	}
	for _, tc := range cases {
		got, err := senderJID(context.Background(), tc.lids, tc.sender)
		if err != nil || got.String() != tc.want {
			t.Errorf("%s: senderJID(%q) = %q, %v; want %q", tc.name, tc.sender, got.String(), err, tc.want)
		}
	}
	if _, err := senderJID(context.Background(), store, ""); err == nil {
		t.Error("an empty sender must be an error")
	}
}

func TestRequestBodyLimit(t *testing.T) {
	handler := newRESTHandler(nil, nil)
	for _, path := range []string{"/api/send", "/api/download"} {
		big := `{"recipient":"` + strings.Repeat("a", maxRequestBody) + `"}`
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(big))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != http.StatusRequestEntityTooLarge {
			t.Errorf("%s with a body over 1 MiB: got %d %q, want 413", path, w.Code, w.Body.String())
		}

		r = httptest.NewRequest(http.MethodPost, path, strings.NewReader("{}"))
		w = httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s with an empty JSON object: got %d %q, want 400", path, w.Code, w.Body.String())
		}
	}
}

func TestAwaitMediaRetry(t *testing.T) {
	waiter := make(chan *events.MediaRetry, 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := awaitMediaRetry(ctx, waiter, time.Hour); err == nil || !strings.Contains(err.Error(), "context canceled") {
		t.Errorf("cancelled context: got %v, want a cancellation error", err)
	}

	if _, err := awaitMediaRetry(context.Background(), waiter, time.Millisecond); err == nil || !strings.Contains(err.Error(), "no answer") {
		t.Errorf("timeout: got %v, want a timeout error", err)
	}

	answer := &events.MediaRetry{MessageID: "X"}
	waiter <- answer
	if got, err := awaitMediaRetry(context.Background(), waiter, time.Hour); err != nil || got != answer {
		t.Errorf("answer: got %v, %v", got, err)
	}
}
