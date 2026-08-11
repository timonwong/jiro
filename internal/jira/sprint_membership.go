package jira

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
)

// SprintMembership records that an Issue belongs or previously belonged to a
// Jira Software Sprint.
type SprintMembership struct {
	ID           int    `json:"id"`
	Name         string `json:"name,omitempty"`
	State        string `json:"state,omitempty"`
	BoardID      int    `json:"boardId,omitempty"`
	Goal         string `json:"goal,omitempty"`
	StartDate    string `json:"startDate,omitempty"`
	EndDate      string `json:"endDate,omitempty"`
	CompleteDate string `json:"completeDate,omitempty"`
}

// SprintMembershipFailure describes one property that could not be normalized
// from a raw Sprint Custom Field entry.
type SprintMembershipFailure struct {
	Index    int    `json:"index"`
	Property string `json:"property,omitempty"`
	Reason   string `json:"reason"`
}

// NormalizeSprintMemberships projects a raw Sprint Custom Field value into
// typed Sprint Memberships while retaining ordered, non-fatal failures.
func NormalizeSprintMemberships(value any) ([]SprintMembership, []SprintMembershipFailure) {
	if value == nil {
		return []SprintMembership{}, nil
	}
	values, ok := value.([]any)
	if !ok {
		values = []any{value}
	}
	memberships := make([]SprintMembership, 0, len(values))
	failures := make([]SprintMembershipFailure, 0)
	for index, value := range values {
		properties, ok := sprintMembershipProperties(value)
		if !ok {
			failures = append(failures, SprintMembershipFailure{Index: index, Reason: "Sprint membership must be a legacy string or object"})
			continue
		}
		membership, entryFailures, ok := normalizeSprintMembership(index, properties)
		failures = append(failures, entryFailures...)
		if ok {
			memberships = append(memberships, membership)
		}
	}
	return memberships, failures
}

func sprintMembershipProperties(value any) (map[string]any, bool) {
	switch value := value.(type) {
	case string:
		return legacySprintProperties(value)
	case map[string]any:
		return value, true
	default:
		return nil, false
	}
}

func legacySprintProperties(raw string) (map[string]any, bool) {
	start := strings.IndexByte(raw, '[')
	end := strings.LastIndexByte(raw, ']')
	if start < 0 || end <= start {
		return nil, false
	}
	properties := make(map[string]any)
	for _, property := range splitLegacySprintProperties(raw[start+1 : end]) {
		key, value, found := strings.Cut(property, "=")
		if found {
			properties[strings.TrimSpace(key)] = value
		}
	}
	return properties, true
}

func splitLegacySprintProperties(value string) []string {
	properties := make([]string, 0, 8)
	start := 0
	for index := 0; index < len(value); index++ {
		if value[index] != ',' {
			continue
		}
		next := index + 1
		for next < len(value) && (value[next] == ' ' || value[next] == '\t') {
			next++
		}
		keyStart := next
		for next < len(value) && legacySprintPropertyCharacter(value[next]) {
			next++
		}
		if next == keyStart || next >= len(value) || value[next] != '=' {
			continue
		}
		properties = append(properties, value[start:index])
		start = index + 1
	}
	properties = append(properties, value[start:])
	return properties
}

func legacySprintPropertyCharacter(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9' || value == '_'
}

func normalizeSprintMembership(index int, properties map[string]any) (SprintMembership, []SprintMembershipFailure, bool) {
	id, present, valid := positiveSprintInteger(properties["id"])
	if !present || !valid {
		return SprintMembership{}, []SprintMembershipFailure{{Index: index, Property: "id", Reason: "positive integer id is required"}}, false
	}
	membership := SprintMembership{ID: id}
	failures := make([]SprintMembershipFailure, 0)
	membership.Name, failures = normalizeSprintString(index, "name", properties["name"], failures)
	membership.State, failures = normalizeSprintState(index, properties["state"], failures)
	boardValue, boardPresent := properties["boardId"]
	if !boardPresent {
		boardValue, boardPresent = properties["rapidViewId"]
	}
	if boardPresent {
		if boardID, present, valid := positiveSprintInteger(boardValue); present && valid {
			membership.BoardID = boardID
		} else if present {
			failures = append(failures, SprintMembershipFailure{Index: index, Property: "boardId", Reason: "positive integer boardId is required"})
		}
	}
	membership.Goal, failures = normalizeSprintString(index, "goal", properties["goal"], failures)
	membership.StartDate, failures = normalizeSprintDate(index, "startDate", properties["startDate"], failures)
	membership.EndDate, failures = normalizeSprintDate(index, "endDate", properties["endDate"], failures)
	membership.CompleteDate, failures = normalizeSprintDate(index, "completeDate", properties["completeDate"], failures)
	return membership, failures, true
}

func positiveSprintInteger(value any) (int, bool, bool) {
	if value == nil {
		return 0, false, false
	}
	switch value := value.(type) {
	case int:
		return value, true, value > 0
	case int64:
		return int(value), true, value > 0 && int64(int(value)) == value
	case float64:
		return int(value), true, value > 0 && value == math.Trunc(value) && float64(int(value)) == value
	case json.Number:
		parsed, err := strconv.Atoi(value.String())
		return parsed, true, err == nil && parsed > 0
	case string:
		value = strings.TrimSpace(value)
		if value == "" || value == "<null>" {
			return 0, false, false
		}
		parsed, err := strconv.Atoi(value)
		return parsed, true, err == nil && parsed > 0
	default:
		return 0, true, false
	}
}

func normalizeSprintString(index int, property string, value any, failures []SprintMembershipFailure) (string, []SprintMembershipFailure) {
	if value == nil {
		return "", failures
	}
	text, ok := value.(string)
	if !ok {
		return "", append(failures, SprintMembershipFailure{Index: index, Property: property, Reason: "string value is required"})
	}
	text = strings.TrimSpace(text)
	if text == "" || text == "<null>" {
		return "", failures
	}
	return text, failures
}

func normalizeSprintState(index int, value any, failures []SprintMembershipFailure) (string, []SprintMembershipFailure) {
	state, failures := normalizeSprintString(index, "state", value, failures)
	if state == "" {
		return "", failures
	}
	state = strings.ToLower(state)
	switch state {
	case string(SprintStateActive), string(SprintStateClosed), string(SprintStateFuture):
		return state, failures
	default:
		return "", append(failures, SprintMembershipFailure{Index: index, Property: "state", Reason: "unsupported Sprint state"})
	}
}

func normalizeSprintDate(index int, property string, value any, failures []SprintMembershipFailure) (string, []SprintMembershipFailure) {
	if value == nil {
		return "", failures
	}
	text, ok := value.(string)
	if !ok {
		return "", append(failures, SprintMembershipFailure{Index: index, Property: property, Reason: "string value is required"})
	}
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || trimmed == "<null>" {
		return "", failures
	}
	return text, failures
}
