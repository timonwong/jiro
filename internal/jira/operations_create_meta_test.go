package jira

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestListCreateFieldsUsesProjectAndIssueTypeScopedEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/rest/api/2/issue/createmeta/OPS/issuetypes/10001" {
			t.Fatalf("request = %s %s", request.Method, request.URL.Path)
		}
		_, _ = io.WriteString(writer, `{"fields":{"summary":{"name":"Summary","required":true,"schema":{"type":"string"},"operations":["set"]},"customfield_10001":{"name":"Sprint","schema":{"type":"array","custom":"com.pyxis.greenhopper.jira:gh-sprint"},"operations":["set"]}}}`)
	}))
	defer server.Close()

	client, err := NewClient(Config{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	fields, err := client.ListCreateFields(context.Background(), "OPS", "10001")
	if err != nil {
		t.Fatal(err)
	}
	want := []CreateField{
		{ID: "customfield_10001", Name: "Sprint", Custom: true, Type: "array", SchemaCustom: "com.pyxis.greenhopper.jira:gh-sprint", Operations: []string{"set"}},
		{ID: "summary", Name: "Summary", Required: true, Type: "string", Operations: []string{"set"}},
	}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("fields = %#v, want %#v", fields, want)
	}
}
