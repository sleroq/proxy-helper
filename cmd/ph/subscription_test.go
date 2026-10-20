package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/sleroq/ph/subscription"
)

func TestAddSubscription(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want subscription.Source
	}{
		{
			name: "URL only",
			args: []string{"https://provider.example/subscription?token=secret"},
			want: subscription.Source{ID: "provider-example", URL: "https://provider.example/subscription?token=secret"},
		},
		{
			name: "explicit ID and prefix",
			args: []string{"--id", "main", "--prefix", "main-", "https://user:secret@provider.example/subscription"},
			want: subscription.Source{ID: "main", Prefix: "main-", URL: "https://user:secret@provider.example/subscription"},
		},
		{
			name: "HTTP and IPv6",
			args: []string{"http://[::1]:18080/subscription"},
			want: subscription.Source{ID: "--1", URL: "http://[::1]:18080/subscription"},
		},
		{
			name: "private URL file",
			args: []string{"--id", "main", "--url-file", "/private/provider-url", "--prefix", "main-"},
			want: subscription.Source{ID: "main", URLFile: "/private/provider-url", Prefix: "main-"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "subscriptions.json")
			stores := []subscription.Store{{Name: "local", Path: path, Writable: true}}
			catalog, err := subscription.Load(stores, "")
			if err != nil {
				t.Fatal(err)
			}
			if err := addSubscription(catalog, tt.args); err != nil {
				t.Fatal(err)
			}

			loaded, err := subscription.Load(stores, "")
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(loaded.Sources(), []subscription.Source{tt.want}) {
				t.Fatal("stored source does not match command arguments")
			}

			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != 0600 {
				t.Fatalf("store mode = %o, want 600", info.Mode().Perm())
			}
		})
	}
}

func TestAddSubscriptionRejectsInvalidArguments(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"https://provider.example/secret", "extra"},
		{"secret"},
		{"ftp://provider.example/secret"},
		{"https:///secret"},
		{"https://provider.example/%secret"},
		{"--id", "invalid.id", "https://provider.example/secret"},
		{"--url-file", "/private/secret"},
		{"--id", "main", "--url-file", "/private/secret", "https://provider.example/secret"},
		{`{"id":"main","url":"https://provider.example/secret"}`},
	} {
		catalog := &subscription.Catalog{}
		err := addSubscription(catalog, args)
		if err == nil {
			t.Fatal("expected invalid arguments to fail")
		}
		if strings.Contains(err.Error(), "secret") {
			t.Fatalf("error exposes URL or credentials: %v", err)
		}
	}
}

func TestAddSubscriptionStoreSelection(t *testing.T) {
	dir := t.TempDir()
	stores := []subscription.Store{
		{Name: "first", Path: filepath.Join(dir, "first.json"), Writable: true},
		{Name: "second", Path: filepath.Join(dir, "second.json"), Writable: true},
	}
	catalog, err := subscription.Load(stores, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := addSubscription(catalog, []string{"https://provider.example/subscription"}); err == nil {
		t.Fatal("expected ambiguous store to fail")
	}
	if err := addSubscription(catalog, []string{"--store", "second", "https://provider.example/subscription"}); err != nil {
		t.Fatal(err)
	}
	if len(catalog.Documents[0].Subscriptions) != 0 || len(catalog.Documents[1].Subscriptions) != 1 {
		t.Fatal("subscription was not added to the selected store")
	}
}
