package jira

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/timonwong/jiro/internal/apperr"
)

func TestResolveFieldSelectorsPreservesOrderedSelection(t *testing.T) {
	t.Parallel()
	fields := []Field{
		{ID: "summary", Name: "Summary"},
		{ID: "customfield_10006", Name: "Story Points", Custom: true},
	}

	selection, err := ResolveFieldSelectors([]string{"summary", "Story Points"}, fields)
	if err != nil {
		t.Fatal(err)
	}
	want := []FieldSelection{
		{Selector: "summary", Resolved: "summary"},
		{Selector: "Story Points", Resolved: "customfield_10006"},
	}
	if !reflect.DeepEqual(selection, want) {
		t.Fatalf("selection = %#v, want %#v", selection, want)
	}
}

func TestResolveFieldSelectorsSupportsDirectSpecialAndExcludedSelectors(t *testing.T) {
	t.Parallel()
	selection, err := ResolveFieldSelectors(
		[]string{"customfield_99999", "*all", "*navigable", "-Story Points"},
		[]Field{{ID: "customfield_10006", Name: "Story Points", Custom: true}},
	)
	if err != nil {
		t.Fatal(err)
	}
	want := []FieldSelection{
		{Selector: "customfield_99999", Resolved: "customfield_99999"},
		{Selector: "*all", Resolved: "*all"},
		{Selector: "*navigable", Resolved: "*navigable"},
		{Selector: "-Story Points", Resolved: "-customfield_10006"},
	}
	if !reflect.DeepEqual(selection, want) {
		t.Fatalf("selection = %#v, want %#v", selection, want)
	}
}

func TestResolveFieldSelectorsPrefersExactIDOverNormalizedName(t *testing.T) {
	t.Parallel()
	selection, err := ResolveFieldSelectors([]string{"status"}, []Field{
		{ID: "status", Name: "Status"},
		{ID: "customfield_10007", Name: "Status", Custom: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := selection[0].Resolved; got != "status" {
		t.Fatalf("resolved = %q, want status", got)
	}
}

func TestResolveFieldSelectorsRejectsNormalizedNameAmbiguity(t *testing.T) {
	t.Parallel()
	_, err := ResolveFieldSelectors([]string{"Story_Points"}, []Field{
		{ID: "customfield_10007", Name: "Story-Points", Custom: true},
		{ID: "customfield_10006", Name: "Story Points", Custom: true},
	})
	if got := apperr.As(err).Kind; got != apperr.KindInvalidInput ||
		!strings.Contains(err.Error(), "customfield_10006, customfield_10007") {
		t.Fatalf("ambiguity error = %v", err)
	}
}

func TestResolveCustomFieldPriorityAndAmbiguity(t *testing.T) {
	t.Parallel()
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		_, _ = w.Write([]byte(`[
			{"id":"customfield_10001","name":"Story Points","custom":true},
			{"id":"customfield_10002","name":"Story-Points","custom":true}
		]`))
	}))
	defer server.Close()
	client, _ := NewClient(Config{BaseURL: server.URL})
	id, err := client.ResolveCustomField(context.Background(), "customfield_12345")
	if err != nil || id != "customfield_12345" || requests != 0 {
		t.Fatalf("direct resolution = %q, %v, requests=%d", id, err, requests)
	}
	_, err = client.ResolveCustomField(context.Background(), "story-points")
	if got := apperr.As(err).Kind; got != apperr.KindInvalidInput || requests != 1 {
		t.Fatalf("ambiguous resolution error = %v, requests=%d", err, requests)
	}
}

func TestResolveCustomFieldAndParseValues(t *testing.T) {
	t.Parallel()
	id, err := ResolveCustomField("故事-点数", []Field{{ID: "customfield_9", Name: "故事 点数", Custom: true}})
	if err != nil || id != "customfield_9" {
		t.Fatalf("ResolveCustomField = %q, %v", id, err)
	}
	values, err := ParseFieldValues([]string{
		"customfield_1=1.25",
		`customfield_2={"value":"High"}`,
		"customfield_3=not-json",
		"customfield_4=true",
	})
	if err != nil {
		t.Fatal(err)
	}
	if number, ok := values["customfield_1"].(json.Number); !ok || number.String() != "1.25" {
		t.Fatalf("number = %#v", values["customfield_1"])
	}
	if object := values["customfield_2"].(map[string]any); object["value"] != "High" {
		t.Fatalf("object = %#v", object)
	}
	if values["customfield_3"] != "not-json" || values["customfield_4"] != true {
		t.Fatalf("values = %#v", values)
	}
}

func TestListFieldsPreservesSchemaCustomIdentity(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/rest/api/2/field" {
			t.Fatalf("request = %s %s", r.Method, r.URL.String())
		}
		_, _ = w.Write([]byte(`[{"id":"customfield_10001","name":"Iteration","custom":true,"schema":{"type":"array","custom":"com.pyxis.greenhopper.jira:gh-sprint"}}]`))
	}))
	defer server.Close()

	client, err := NewClient(Config{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	fields, err := client.ListFields(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []Field{{
		ID: "customfield_10001", Name: "Iteration", Custom: true, Type: "array",
		SchemaCustom: "com.pyxis.greenhopper.jira:gh-sprint",
	}}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("fields = %#v, want %#v", fields, want)
	}
}

func TestFieldSelectorSelectsMetadataField(t *testing.T) {
	t.Parallel()
	tests := []struct {
		selector string
		want     bool
	}{
		{selector: "Sprint", want: true},
		{selector: "summary", want: true},
		{selector: "-Sprint", want: false},
		{selector: "customfield_10001", want: false},
		{selector: "*all", want: false},
		{selector: "*navigable", want: false},
	}
	for _, test := range tests {
		got, err := FieldSelectorSelectsMetadataField(test.selector)
		if err != nil || got != test.want {
			t.Fatalf("FieldSelectorSelectsMetadataField(%q) = %t, %v; want %t", test.selector, got, err, test.want)
		}
	}
}
