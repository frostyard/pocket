package app

import (
	"os"
	"strings"
	"time"
	_ "time/tzdata" // Packaged clients need IANA zones even without a system zone database.
)

// localTimeZone finds a portable IANA name rather than a seasonal abbreviation.
// If the platform exposes none, omitting the zone lets the server choose its own.
func localTimeZone() string {
	if zone := ianaZone(os.Getenv("TZ")); zone != "" {
		return zone
	}
	if target, err := os.Readlink("/etc/localtime"); err == nil {
		if zone := ianaZone(target); zone != "" {
			return zone
		}
	}
	return ianaZone(time.Local.String())
}

func ianaZone(name string) string {
	name = strings.TrimPrefix(name, ":")
	if _, zone, ok := strings.Cut(name, "zoneinfo/"); ok {
		name = zone
	}
	if name == "" || name == "Local" {
		return ""
	}
	if _, err := time.LoadLocation(name); err != nil {
		return ""
	}
	return name
}
