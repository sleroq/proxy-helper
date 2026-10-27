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
			response, err := fetchResponse(context.Background(), Source{ID: "test", URL: "https://example.com/secret"}, client, "")
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
	_, err := fetchResponse(ctx, Source{ID: "test", URL: "https://example.com/secret"}, client, "")
	if err == nil || err.Error() != "source test: fetch failed" {
		t.Fatalf("error = %v", err)
	}
}

func TestFetchDeviceRejection(t *testing.T) {
	for _, test := range []struct {
		name    string
		headers http.Header
		want    string
	}{
		{"missing device ID", http.Header{"X-Hwid-Not-Supported": {"true"}, "X-Hwid-Limit": {"true"}},
			"source test: provider requires a device ID; configure hwid"},
		{"device limit", http.Header{"X-Hwid-Max-Devices-Reached": {"true"}},
			"source test: provider device limit reached; reuse an existing device ID or free a device slot"},
		{"legacy device limit", http.Header{"X-Hwid-Limit": {"true"}},
			"source test: provider device limit reached; reuse an existing device ID or free a device slot"},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, status := range []int{http.StatusOK, http.StatusNotFound} {
				body := &fetchBody{Reader: strings.NewReader("private provider message")}
				client := &http.Client{Transport: fetchTransport(func(*http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: status, Header: test.headers, Body: body}, nil
				})}
				_, err := fetchResponse(context.Background(), Source{ID: "test", URL: "https://example.com/secret"}, client, "")
				if err == nil || err.Error() != test.want || !body.closed {
					t.Fatalf("error = %v, closed = %v", err, body.closed)
				}
			}
		})
	}
}

func TestNativeFetchDeviceID(t *testing.T) {
	const hwid = "test-device-123456"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Hwid") != hwid {
			w.Header().Set("X-Hwid-Not-Supported", "true")
			w.Header().Set("X-Hwid-Limit", "true")
			_, _ = io.WriteString(w, "vless://uuid@0.0.0.0:1?security=none&type=tcp#unsupported")
			return
		}
		w.Header().Set("X-Hwid-Active", "true")
		w.Header().Set("X-Hwid-Not-Supported", "false")
		w.Header().Set("X-Hwid-Limit", "false")
		_, _ = io.WriteString(w, "vless://uuid@node.example:443?security=tls&type=tcp#node")
	}))
	defer server.Close()
	source := Source{ID: "test", URL: server.URL}
	if _, err := (Native{}).Fetch(context.Background(), source); err == nil || !strings.Contains(err.Error(), "configure hwid") {
		t.Fatalf("missing device ID: %v", err)
	}
	for range 2 {
		nodes, err := (Native{HWID: hwid}).Fetch(context.Background(), source)
		if err != nil || len(nodes) != 1 {
			t.Fatalf("fetch with device ID: count = %d, error = %v", len(nodes), err)
		}
		if nodes[0].String("server") != "node.example" {
			t.Fatal("wrong node")
		}
	}
}

func TestFetchInvalidDeviceID(t *testing.T) {
	source := Source{ID: "test", URL: "https://example.com/secret"}
	client := &http.Client{Transport: fetchTransport(func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid device ID must not reach the network")
		return nil, errors.New("unexpected request")
	})}
	for _, value := range []string{"short", strings.Repeat("a", 65), "invalid space", "invalid\nnewline"} {
		_, err := fetchResponse(context.Background(), source, client, value)
		if err == nil || err.Error() != "source test: invalid hwid; expected 10-64 letters, digits, = or -" {
			t.Fatalf("invalid ID: %v", err)
		}
	}
}

func TestNativeRedirectDeviceID(t *testing.T) {
	const hwid = "test-device-123456"
	for _, mode := range []string{"same origin", "cross origin", "HTTPS downgrade"} {
		t.Run(mode, func(t *testing.T) {
			received := make(chan string, 1)
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				received <- r.Header.Get("X-Hwid")
				_, _ = io.WriteString(w, "trojan://password@node.example:443#node")
			}))
			defer target.Close()
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/redirected" {
					received <- r.Header.Get("X-Hwid")
					_, _ = io.WriteString(w, "trojan://password@node.example:443#node")
					return
				}
				destination := target.URL
				if mode == "same origin" {
					destination = "/redirected"
				}
				http.Redirect(w, r, destination, http.StatusFound)
			})
			var origin *httptest.Server
			if mode == "HTTPS downgrade" {
				origin = httptest.NewTLSServer(handler)
			} else {
				origin = httptest.NewServer(handler)
			}
			defer origin.Close()

			native := Native{Client: origin.Client(), HWID: hwid}
			_, err := native.Fetch(context.Background(), Source{ID: "test", URL: origin.URL})
			if err != nil {
				t.Fatal(err)
			}
			want := ""
			if mode == "same origin" {
				want = hwid
			}
			if got := <-received; got != want {
				t.Fatal("redirect device ID did not match origin policy")
			}
		})
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
