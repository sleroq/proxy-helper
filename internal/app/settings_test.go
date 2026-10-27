package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/sleroq/ph/internal/files"
)

func TestSettingsDefaultHWID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := Init(path, "sing-box", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	var initialized Settings
	if err := files.Read(path, &initialized); err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(initialized.HWID) {
		t.Fatal("init must write the default hwid into the config")
	}

	const config = `{"state_dir":"state","template_file":"template.json"}`
	if err := os.WriteFile(path, []byte(config), 0644); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		loaded, err := LoadSettings(path)
		if err != nil {
			t.Fatal(err)
		}
		if loaded.HWID != initialized.HWID {
			t.Fatal("omitted hwid must use the same stable default")
		}
	}
	unchanged, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(unchanged) != config {
		t.Fatal("loading defaults must not rewrite the config")
	}
}
