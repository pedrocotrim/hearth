package web

import (
	"strings"
	"time"
)

func cleanName(s string, max int) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" || len([]rune(s)) > max || hasControl(s) {
		return "", false
	}
	return s, true
}

func cleanOptional(s string, max int) (string, bool) {
	s = strings.TrimSpace(s)
	if len([]rune(s)) > max || hasControl(s) {
		return "", false
	}
	return s, true
}

func cleanNotes(s string, max int) (string, bool) {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.TrimSpace(s)
	if len([]rune(s)) > max {
		return "", false
	}
	for _, r := range s {
		if r != '\n' && r != '\t' && (r < 32 || r == 127) {
			return "", false
		}
	}
	return s, true
}

func cleanEmail(s string) (string, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if len(s) < 3 || len(s) > 254 || strings.ContainsAny(s, " \t\r\n") {
		return "", false
	}
	local, domain, ok := strings.Cut(s, "@")
	if !ok || local == "" || strings.Contains(domain, " ") || !strings.Contains(domain, ".") || strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") {
		return "", false
	}
	return s, true
}

func hasControl(s string) bool {
	for _, r := range s {
		if r < 32 || r == 127 {
			return true
		}
	}
	return false
}

func parseDateField(s string) (time.Time, error) {
	t, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(s), time.Local)
	if err != nil {
		return time.Time{}, err
	}
	if t.Year() < 2000 || t.Year() > 2100 {
		return time.Time{}, errOutOfRange
	}
	return t, nil
}
