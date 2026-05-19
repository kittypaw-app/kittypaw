package core

import (
	"testing"
	"time"
)

func TestResolveUserTimezoneUsesConfiguredIANAZone(t *testing.T) {
	cfg := DefaultConfig()
	cfg.User.Timezone = "America/New_York"

	tz := ResolveUserTimezone(&cfg)

	if !tz.Configured {
		t.Fatal("Configured = false, want true")
	}
	if !tz.Valid {
		t.Fatal("Valid = false, want true")
	}
	if tz.Name != "America/New_York" {
		t.Fatalf("Name = %q, want America/New_York", tz.Name)
	}
	if tz.Location == nil || tz.Location.String() != "America/New_York" {
		t.Fatalf("Location = %v, want America/New_York", tz.Location)
	}
}

func TestResolveUserTimezoneFallsBackToUTCForInvalidConfiguredZone(t *testing.T) {
	cfg := DefaultConfig()
	cfg.User.Timezone = "Mars/Olympus"

	tz := ResolveUserTimezone(&cfg)

	if !tz.Configured {
		t.Fatal("Configured = false, want true")
	}
	if tz.Valid {
		t.Fatal("Valid = true, want false")
	}
	if tz.Name != "UTC" {
		t.Fatalf("Name = %q, want UTC", tz.Name)
	}
	if tz.Location != time.UTC {
		t.Fatalf("Location = %v, want UTC", tz.Location)
	}
}

func TestResolveUserTimezoneUsesProcessLocalWhenUnset(t *testing.T) {
	cfg := DefaultConfig()

	tz := ResolveUserTimezone(&cfg)

	if tz.Configured {
		t.Fatal("Configured = true, want false")
	}
	if !tz.Valid {
		t.Fatal("Valid = false, want true")
	}
	if tz.Location != time.Local {
		t.Fatalf("Location = %v, want time.Local", tz.Location)
	}
}
