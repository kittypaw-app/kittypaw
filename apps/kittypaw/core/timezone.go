package core

import (
	"strings"
	"time"
)

// UserTimezone is the resolved timezone policy for a user/account config.
type UserTimezone struct {
	Name       string
	Location   *time.Location
	Configured bool
	Valid      bool
}

// ResolveUserTimezone resolves Config.User.Timezone into a Location.
//
// Empty timezone preserves the historical local-daemon behavior by using
// time.Local. An explicitly configured but invalid timezone falls back to UTC
// so a bad account config does not silently schedule against the host locale.
func ResolveUserTimezone(cfg *Config) UserTimezone {
	name := ""
	if cfg != nil {
		name = strings.TrimSpace(cfg.User.Timezone)
	}
	if name == "" {
		return UserTimezone{
			Name:     localTimezoneName(),
			Location: time.Local,
			Valid:    true,
		}
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return UserTimezone{
			Name:       "UTC",
			Location:   time.UTC,
			Configured: true,
			Valid:      false,
		}
	}
	return UserTimezone{
		Name:       name,
		Location:   loc,
		Configured: true,
		Valid:      true,
	}
}

func localTimezoneName() string {
	name := strings.TrimSpace(time.Local.String())
	if name == "" {
		return "Local"
	}
	return name
}
