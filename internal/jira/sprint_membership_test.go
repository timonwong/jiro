package jira

import (
	"reflect"
	"testing"
)

func TestNormalizeSprintMembershipsLegacyValue(t *testing.T) {
	t.Parallel()

	raw := []any{
		"com.atlassian.greenhopper.service.sprint.Sprint@abc[id=1839,rapidViewId=12,state=ACTIVE,name=AIT-4.5-S2,goal=Ship the release,startDate=2026-08-01T09:00:00.000+0800,endDate=<null>,completeDate=2026-08-11T18:30:00.000+0800,sequence=1839]",
	}

	memberships, failures := NormalizeSprintMemberships(raw)
	if len(failures) != 0 {
		t.Fatalf("failures = %#v", failures)
	}
	want := []SprintMembership{{
		ID: 1839, Name: "AIT-4.5-S2", State: "active", BoardID: 12, Goal: "Ship the release",
		StartDate: "2026-08-01T09:00:00.000+0800", CompleteDate: "2026-08-11T18:30:00.000+0800",
	}}
	if !reflect.DeepEqual(memberships, want) {
		t.Fatalf("memberships = %#v, want %#v", memberships, want)
	}
}

func TestNormalizeSprintMembershipsStructuredAndBestEffort(t *testing.T) {
	t.Parallel()

	raw := []any{
		map[string]any{
			"id": float64(1840), "name": "AIT-4.5-S3", "state": "FUTURE", "boardId": float64(12),
			"endDate": nil, "sequence": float64(7),
		},
		"com.atlassian.greenhopper.service.sprint.Sprint@broken[name=Missing ID]",
		"com.atlassian.greenhopper.service.sprint.Sprint@duplicate[id=1840,name=AIT-4.5-S3,state=BROKEN,rapidViewId=abc]",
		"broken",
		map[string]any{"id": float64(1841), "name": float64(9)},
	}

	memberships, failures := NormalizeSprintMemberships(raw)
	wantMemberships := []SprintMembership{
		{ID: 1840, Name: "AIT-4.5-S3", State: "future", BoardID: 12},
		{ID: 1840, Name: "AIT-4.5-S3"},
		{ID: 1841},
	}
	if !reflect.DeepEqual(memberships, wantMemberships) {
		t.Fatalf("memberships = %#v, want %#v", memberships, wantMemberships)
	}
	wantFailures := []SprintMembershipFailure{
		{Index: 1, Property: "id", Reason: "positive integer id is required"},
		{Index: 2, Property: "state", Reason: "unsupported Sprint state"},
		{Index: 2, Property: "boardId", Reason: "positive integer boardId is required"},
		{Index: 3, Reason: "Sprint membership must be a legacy string or object"},
		{Index: 4, Property: "name", Reason: "string value is required"},
	}
	if !reflect.DeepEqual(failures, wantFailures) {
		t.Fatalf("failures = %#v, want %#v", failures, wantFailures)
	}
}

func TestNormalizeSprintMembershipsPreservesCommasInsideLegacyValues(t *testing.T) {
	t.Parallel()

	raw := []any{
		"com.atlassian.greenhopper.service.sprint.Sprint@abc[id=1839,state=ACTIVE,name=AIT, Platform S2,goal=Ship code, docs,sequence=1839]",
	}
	memberships, failures := NormalizeSprintMemberships(raw)
	if len(failures) != 0 {
		t.Fatalf("failures = %#v", failures)
	}
	want := []SprintMembership{{ID: 1839, Name: "AIT, Platform S2", State: "active", Goal: "Ship code, docs"}}
	if !reflect.DeepEqual(memberships, want) {
		t.Fatalf("memberships = %#v, want %#v", memberships, want)
	}
}

func TestNormalizeSprintMembershipsAcceptsSingletonRepresentationsAndWarnsOnUnsupportedScalar(t *testing.T) {
	t.Parallel()

	memberships, failures := NormalizeSprintMemberships(map[string]any{"id": float64(9), "state": "CLOSED"})
	if !reflect.DeepEqual(memberships, []SprintMembership{{ID: 9, State: "closed"}}) || len(failures) != 0 {
		t.Fatalf("structured singleton memberships=%#v failures=%#v", memberships, failures)
	}

	memberships, failures = NormalizeSprintMemberships(float64(9))
	if len(memberships) != 0 || !reflect.DeepEqual(failures, []SprintMembershipFailure{{Index: 0, Reason: "Sprint membership must be a legacy string or object"}}) {
		t.Fatalf("unsupported scalar memberships=%#v failures=%#v", memberships, failures)
	}

	memberships, failures = NormalizeSprintMemberships(nil)
	if len(memberships) != 0 || len(failures) != 0 {
		t.Fatalf("nil memberships=%#v failures=%#v", memberships, failures)
	}
}

func TestNormalizeSprintMembershipsPreservesDateStringsExactly(t *testing.T) {
	t.Parallel()

	date := " 2026-08-11T18:30:00.000+0800 "
	values := []any{
		map[string]any{"id": float64(9), "startDate": date},
		"Sprint@[id=10,startDate=" + date + "]",
	}
	memberships, failures := NormalizeSprintMemberships(values)
	want := []SprintMembership{{ID: 9, StartDate: date}, {ID: 10, StartDate: date}}
	if len(failures) != 0 || !reflect.DeepEqual(memberships, want) {
		t.Fatalf("memberships=%#v want=%#v failures=%#v", memberships, want, failures)
	}
}

func TestNormalizeSprintMembershipsRejectsNonPositiveAndFractionalIDs(t *testing.T) {
	t.Parallel()

	values := []any{
		"Sprint@[id=0]",
		"Sprint@[id=-1]",
		map[string]any{"id": 1.5},
	}
	memberships, failures := NormalizeSprintMemberships(values)
	if len(memberships) != 0 || !reflect.DeepEqual(failures, []SprintMembershipFailure{
		{Index: 0, Property: "id", Reason: "positive integer id is required"},
		{Index: 1, Property: "id", Reason: "positive integer id is required"},
		{Index: 2, Property: "id", Reason: "positive integer id is required"},
	}) {
		t.Fatalf("memberships=%#v failures=%#v", memberships, failures)
	}
}
