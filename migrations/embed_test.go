package migrations

import (
	"io/fs"
	"strings"
	"testing"
)

func TestEmbeddedMigrationsAreGooseFiles(t *testing.T) {
	entries, err := fs.Glob(FS, "*.sql")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no migrations embedded")
	}
	for _, name := range entries {
		content, err := fs.ReadFile(FS, name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		body := string(content)
		if !strings.Contains(body, "-- +goose Up") || !strings.Contains(body, "-- +goose Down") {
			t.Errorf("%s is missing a goose Up/Down section", name)
		}
	}
}
