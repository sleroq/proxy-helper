package integration_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
)

func TestSubscriptionDeviceID(t *testing.T) {
	for _, hwid := range []string{"", "configured-device-123456"} {
		name := "default"
		if hwid != "" {
			name = "configured"
		}
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			if hwid != "" {
				f.config["hwid"] = hwid
				f.write("config.json", f.config)
			}
			received := make(chan string, 2)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				received <- r.Header.Get("X-Hwid")
				_, _ = io.WriteString(w, "trojan://password@node.example:443#node")
			}))
			defer server.Close()
			f.write("declared.json", map[string]any{"subscriptions": []any{
				map[string]any{"id": "main", "url": server.URL},
			}})

			f.run("", true, "update")
			f.run("", true, "update")
			first, second := <-received, <-received
			if !regexp.MustCompile(`^[a-zA-Z0-9=-]{10,64}$`).MatchString(first) || first != second {
				t.Fatal("updates must send a valid, stable device ID")
			}
			if hwid != "" && first != hwid {
				t.Fatal("configured hwid must override the default")
			}
		})
	}
}
