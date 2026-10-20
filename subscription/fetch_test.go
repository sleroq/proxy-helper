package subscription

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fetchTransport func(*http.Request) (*http.Response, error)

func (f fetchTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type fetchBody struct {
	io.Reader
	closed bool
}

func (b *fetchBody) Close() error { b.closed = true; return nil }

func TestFetchResponse(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		length int64
		want   string
	}{
		{"success", 200, -1, ""},
		{"status", 403, 0, "source test: HTTP 403"},
		{"size", 200, responseLimit + 1, "source test: subscription response too large"},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := &fetchBody{Reader: strings.NewReader("body")}
			client := &http.Client{Transport: fetchTransport(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: test.status, ContentLength: test.length, Body: body}, nil
			})}
			response, err := fetchResponse(context.Background(), Source{ID: "test", URL: "https://example.com/secret"}, client)
			if test.want != "" {
				if err == nil || err.Error() != test.want || !body.closed {
					t.Fatalf("error = %v, closed = %v", err, body.closed)
				}
				if test.name == "size" && !errors.Is(err, errResponseTooLarge) {
					t.Fatal("lost size sentinel")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if body.closed {
				t.Fatal("success body closed prematurely")
			}
			_ = response.Body.Close()
		})
	}
}

func TestFetchResponseCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client := &http.Client{Transport: fetchTransport(func(r *http.Request) (*http.Response, error) {
		if r.Context().Err() != context.Canceled {
			t.Fatal("request lost cancellation")
		}
		return nil, errors.New("secret credentials")
	})}
	_, err := fetchResponse(ctx, Source{ID: "test", URL: "https://example.com/secret"}, client)
	if err == nil || err.Error() != "source test: fetch failed" {
		t.Fatalf("error = %v", err)
	}
}

func TestNativeRedirectPreservesClient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/redirected", http.StatusFound)
	}))
	defer server.Close()
	calls := 0
	client := &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
		calls++
		return http.ErrUseLastResponse
	}}
	_, err := (Native{Client: client}).Fetch(context.Background(), Source{ID: "test", URL: server.URL})
	if err == nil || err.Error() != "source test: HTTP 302" || calls != 1 {
		t.Fatalf("error = %v, calls = %d", err, calls)
	}
	if client.Timeout != 3*time.Second {
		t.Fatal("client timeout changed")
	}
	req := httptest.NewRequest(http.MethodGet, "file:///secret", nil)
	if err := client.CheckRedirect(req, nil); !errors.Is(err, http.ErrUseLastResponse) {
		t.Fatalf("client redirect callback changed: %v", err)
	}
	if err := nativeRedirect(client.CheckRedirect)(req, nil); err == nil || calls != 2 {
		t.Fatalf("non-HTTP redirect reached callback: %v, calls = %d", err, calls)
	}
}
