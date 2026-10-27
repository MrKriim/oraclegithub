package main

import (
	"fmt"
	"time"
)

// formatTwitterTime - Shows real seconds, minutes, hours etc.
// formatTwitterTime - Shows real seconds, minutes, hours etc.
// formatTwitterTime - Fixes the 5h/4h database parsing shift
func formatTwitterTime(t time.Time) string {
	// 1. Get your local target location
	loc, err := time.LoadLocation("Asia/Karachi")
	if err != nil {
		loc = time.FixedZone("PKT", 5*60*60)
	}

	// 2. Get current time in Karachi
	now := time.Now().In(loc)

	// 3. FORCE Go to interpret the database time numbers as Karachi time
	// This stops Go from mistakenly treating "16:33" from Postgres as UTC.
	then := time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), loc)

	diff := now.Unix() - then.Unix()

	if diff < 0 {
		diff = -diff
	}

	// Show actual seconds for first minute
	if diff < 60 {
		if diff <= 5 {
			return "Just now"
		}
		return fmt.Sprintf("%ds ago", diff)
	}
	if diff < 3600 {
		return fmt.Sprintf("%dm ago", diff/60)
	}
	if diff < 86400 {
		return fmt.Sprintf("%dh ago", diff/3600)
	}

	days := diff / 86400
	if days < 7 {
		return fmt.Sprintf("%dd ago", days)
	}
	if days < 30 {
		return fmt.Sprintf("%dw ago", days/7)
	}
	if days < 365 {
		return fmt.Sprintf("%dmo ago", days/30)
	}
	return fmt.Sprintf("%dy ago", days/365)
}
