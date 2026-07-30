package userpath

import (
	"fmt"
	"strings"
)

type Action string

const (
	Add    Action = "add"
	Remove Action = "remove"
)

type Result string

const (
	Added   Result = "added"
	Present Result = "present"
	Removed Result = "removed"
	Absent  Result = "absent"
)

func Apply(current, entry string, action Action) (string, Result, error) {
	normalizedEntry := normalizeEntry(entry)
	if normalizedEntry == "" {
		return current, "", fmt.Errorf("PATH entry is empty")
	}

	parts := strings.Split(current, ";")
	found := false
	for _, part := range parts {
		if strings.EqualFold(normalizeEntry(part), normalizedEntry) {
			found = true
			break
		}
	}

	switch action {
	case Add:
		if found {
			return current, Present, nil
		}
		if current == "" || strings.HasSuffix(current, ";") {
			return current + entry, Added, nil
		}
		return current + ";" + entry, Added, nil
	case Remove:
		if !found {
			return current, Absent, nil
		}
		kept := make([]string, 0, len(parts))
		for _, part := range parts {
			if !strings.EqualFold(normalizeEntry(part), normalizedEntry) {
				kept = append(kept, part)
			}
		}
		onlyEmpty := true
		for _, part := range kept {
			if normalizeEntry(part) != "" {
				onlyEmpty = false
				break
			}
		}
		if onlyEmpty {
			return "", Removed, nil
		}
		return strings.Join(kept, ";"), Removed, nil
	default:
		return current, "", fmt.Errorf("unsupported PATH action %q", action)
	}
}

func normalizeEntry(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		value = value[1 : len(value)-1]
	}
	for len(value) > 3 && (value[len(value)-1] == '\\' || value[len(value)-1] == '/') {
		value = value[:len(value)-1]
	}
	return value
}
