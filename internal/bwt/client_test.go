package bwt

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newTestClient points a client at a stub server and turns off the pacing that
// exists for the real API's sake.
func newTestClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return New("test-key", WithEndpoint(srv.URL), WithThrottle(0))
}

// Everything the API returns is wrapped in a single "d" member, and write calls
// answer {"d":null}.
func TestUnwrapsEnvelope(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"d":[{"__type":"Site:#Microsoft.Bing.Webmaster.Api","Url":"https://example.com/","IsVerified":true}]}`)
	})

	sites, err := c.Sites(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sites) != 1 || sites[0].URL != "https://example.com/" || !sites[0].IsVerified {
		t.Fatalf("got %+v", sites)
	}
}

func TestNullEnvelopeIsNotAnError(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"d":null}`)
	})
	if err := c.SubmitURL(context.Background(), "https://example.com/", "https://example.com/a"); err != nil {
		t.Fatalf("a null envelope means success, got: %v", err)
	}
}

// The key belongs in the query string and the payload in the body -- the API
// rejects the request outright if they are swapped.
func TestRequestShape(t *testing.T) {
	var (
		gotMethod string
		gotPath   string
		gotKey    string
		gotType   string
		gotBody   map[string]any
	)
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		gotKey = r.URL.Query().Get("apikey")
		gotType = r.Header.Get("Content-Type")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = io.WriteString(w, `{"d":null}`)
	})

	if err := c.SubmitURLBatch(context.Background(), "https://example.com/", []string{"https://example.com/a"}); err != nil {
		t.Fatal(err)
	}

	if gotMethod != http.MethodPost || !strings.HasSuffix(gotPath, "/SubmitUrlBatch") {
		t.Errorf("%s %s, want POST .../SubmitUrlBatch", gotMethod, gotPath)
	}
	if gotKey != "test-key" {
		t.Errorf("apikey = %q, want it in the query string", gotKey)
	}
	if !strings.HasPrefix(gotType, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", gotType)
	}
	if gotBody["siteUrl"] != "https://example.com/" {
		t.Errorf("body = %+v, want siteUrl in the JSON body", gotBody)
	}
	if list, ok := gotBody["urlList"].([]any); !ok || len(list) != 1 {
		t.Errorf("body = %+v, want urlList as an array", gotBody)
	}
}

func TestGetSendsParamsInQueryString(t *testing.T) {
	var got string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query().Get("siteUrl")
		_, _ = io.WriteString(w, `{"d":[]}`)
	})
	if _, err := c.QueryStats(context.Background(), "https://example.com/"); err != nil {
		t.Fatal(err)
	}
	if got != "https://example.com/" {
		t.Errorf("siteUrl = %q", got)
	}
}

// Bing answers a rejected call with an HTTP 4xx carrying its own ErrorCode,
// which is more specific than the status: the message must name the real cause.
func TestErrorCarriesActionableHint(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"ErrorCode":3,"Message":"Invalid API key"}`)
	})

	_, err := c.Sites(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	msg := err.Error()
	for _, want := range []string{"Invalid API key", "InvalidApiKey", "bwt auth set"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error is missing %q:\n%s", want, msg)
		}
	}
}

func TestErrorHintForMissingPermission(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"ErrorCode":14,"Message":"Not authorized"}`)
	})

	_, err := c.QueryStats(context.Background(), "https://example.com/")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "bwt sites list") {
		t.Errorf("a permission error should point at the site list:\n%s", err)
	}
}

// Some error bodies arrive as a JSON string that itself contains JSON.
func TestErrorParsesDoubleEncodedBody(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(`{"ErrorCode":7,"Message":"Invalid url"}`)
	})

	_, err := c.Sites(context.Background())
	if err == nil || !strings.Contains(err.Error(), "Invalid url") {
		t.Fatalf("got %v, want the nested message to be surfaced", err)
	}
}

// A throttle is transient: the client should back off and try again rather than
// handing the user an error they can do nothing about.
func TestRetriesThrottleThenSucceeds(t *testing.T) {
	calls := 0
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"ErrorCode":4,"Message":"Throttled"}`)
			return
		}
		_, _ = io.WriteString(w, `{"d":[]}`)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := c.Sites(ctx); err != nil {
		t.Fatalf("the retry should have succeeded: %v", err)
	}
	if calls != 2 {
		t.Errorf("made %d calls, want 2 (one throttled, one retried)", calls)
	}
}

// A bad parameter will never succeed on a retry; retrying just wastes the
// user's time and the API's budget.
func TestDoesNotRetryPermanentErrors(t *testing.T) {
	calls := 0
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"ErrorCode":8,"Message":"Invalid parameter"}`)
	})

	if _, err := c.Sites(context.Background()); err == nil {
		t.Fatal("expected an error")
	}
	if calls != 1 {
		t.Errorf("made %d calls, want exactly 1", calls)
	}
}

func TestChildrenURLInfoSendsRequiredFilterObject(t *testing.T) {
	var body map[string]any
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = io.WriteString(w, `{"d":[]}`)
	})

	if _, err := c.ChildrenURLInfo(context.Background(), "https://example.com/", "https://example.com/blog/", 2); err != nil {
		t.Fatal(err)
	}
	if body["page"] != float64(2) {
		t.Errorf("page = %v, want 2", body["page"])
	}
	// The API rejects the call without this object, even when nothing is filtered.
	fp, ok := body["filterProperties"].(map[string]any)
	if !ok {
		t.Fatalf("body = %+v, want a filterProperties object", body)
	}
	if fp["__type"] == nil || fp["HttpCodeFilters"] != float64(0) {
		t.Errorf("filterProperties = %+v, want the typed all-pass filter", fp)
	}
}

func TestMissingAPIKeyIsRejectedBeforeSending(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	defer srv.Close()

	c := New("", WithEndpoint(srv.URL), WithThrottle(0))
	if _, err := c.Sites(context.Background()); err == nil {
		t.Fatal("expected an error")
	}
	if calls != 0 {
		t.Error("no request should be sent without an API key")
	}
}
