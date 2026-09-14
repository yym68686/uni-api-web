package main

import (
	"fmt"
	"time"
)

var rangeNames = []string{"5m", "15m", "1h", "24h", "7d", "30d", "today", "week", "month", "year", "all"}

func rangeStart(name string, now time.Time, zone *time.Location) (time.Time, error) {
	local := now.In(zone)
	midnight := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, zone)
	switch name {
	case "5m":
		return now.Add(-5 * time.Minute), nil
	case "15m":
		return now.Add(-15 * time.Minute), nil
	case "1h":
		return now.Add(-time.Hour), nil
	case "24h":
		return now.Add(-24 * time.Hour), nil
	case "7d":
		return now.Add(-7 * 24 * time.Hour), nil
	case "30d":
		return now.Add(-30 * 24 * time.Hour), nil
	case "today":
		return midnight, nil
	case "week":
		days := (int(local.Weekday()) + 6) % 7
		return midnight.AddDate(0, 0, -days), nil
	case "month":
		return time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, zone), nil
	case "year":
		return time.Date(local.Year(), 1, 1, 0, 0, 0, 0, zone), nil
	case "all":
		return time.Unix(0, 0), nil
	default:
		return time.Time{}, fmt.Errorf("unknown time range")
	}
}
