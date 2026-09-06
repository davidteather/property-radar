package config_test

import (
	"testing"
	"time"

	"github.com/davidteather/property-radar/internal/config"
)

func TestLoadPhotoTTL(t *testing.T) {
	const day = 24 * time.Hour

	cases := []struct {
		name    string
		env     string
		want    time.Duration
		wantErr bool
	}{
		{name: "default is 30 days", env: "", want: 30 * day},
		{name: "override in days", env: "7", want: 7 * day},
		{name: "zero disables eviction", env: "0", want: 0},
		{name: "unparseable is rejected", env: "notanumber", wantErr: true},
		{name: "negative is rejected", env: "-5", wantErr: true},
		{name: "over the cap is rejected", env: "3651", wantErr: true},
		{name: "overflowing day count is rejected", env: "9223372036854775807", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DATABASE_URL", "")
			t.Setenv("STORAGE_BACKEND", "")
			t.Setenv("PHOTO_TTL_DAYS", tc.env)

			cfg, err := config.Load()
			if tc.wantErr {
				if err == nil {
					t.Fatalf("PHOTO_TTL_DAYS=%q should be rejected", tc.env)
				}
				return
			}
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if cfg.PhotoTTL != tc.want {
				t.Fatalf("PhotoTTL = %v, want %v", cfg.PhotoTTL, tc.want)
			}
		})
	}
}
