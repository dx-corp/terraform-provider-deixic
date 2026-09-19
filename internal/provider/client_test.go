package provider

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	consolev1 "buf.build/gen/go/evalops-infra/proto/protocolbuffers/go/console/v1"
)

func TestClientUsesBinaryConnectContract(t *testing.T) {
	fixture := newContractServer(t)
	server := httptest.NewServer(fixture)
	defer server.Close()
	client, err := NewClient(server.URL, fixture.token, fixture.organizationID, fixture.workspaceID, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	response := &consolev1.CreateBusinessObjectResponse{}
	err = client.Invoke(context.Background(), "CreateBusinessObject", &consolev1.CreateBusinessObjectRequest{
		OrganizationId: fixture.organizationID,
		WorkspaceId:    fixture.workspaceID,
		IdempotencyKey: "test-idempotency",
		TypeId:         "customer",
		SchemaRevision: 1,
		Values: []*consolev1.BusinessFieldValue{{
			FieldId: "name",
			Value:   &consolev1.BusinessFieldValue_Text{Text: "Acme"},
		}},
	}, response)
	if err != nil {
		t.Fatal(err)
	}
	if response.GetObject().GetObjectId() == "" || response.GetObject().GetRevision() != 1 {
		t.Fatalf("unexpected create response: %v", response.GetObject())
	}
}

func TestClientClassifiesConnectNotFound(t *testing.T) {
	fixture := newContractServer(t)
	server := httptest.NewServer(fixture)
	defer server.Close()
	client, err := NewClient(server.URL, fixture.token, fixture.organizationID, fixture.workspaceID, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	err = client.Invoke(context.Background(), "GetBusinessObject", &consolev1.GetBusinessObjectRequest{
		OrganizationId: fixture.organizationID,
		WorkspaceId:    fixture.workspaceID,
		ObjectId:       "missing",
	}, &consolev1.GetBusinessObjectResponse{})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestClientDoesNotClassifyUntrusted404AsMissing(t *testing.T) {
	for _, test := range []struct {
		name        string
		contentType string
		protocol    string
		body        string
	}{
		{name: "proxy html", contentType: "text/html", body: "<h1>Not Found</h1>"},
		{name: "json without Connect version", contentType: "application/json", body: `{"code":"not_found","message":"proxy miss"}`},
		{name: "malformed Connect JSON", contentType: "application/json", protocol: "1", body: `{"code":"not_found"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", test.contentType)
				if test.protocol != "" {
					writer.Header().Set("Connect-Protocol-Version", test.protocol)
				}
				writer.WriteHeader(http.StatusNotFound)
				_, _ = writer.Write([]byte(test.body))
			}))
			defer server.Close()
			client, err := NewClient(server.URL, "token", "org", "workspace", server.Client())
			if err != nil {
				t.Fatal(err)
			}
			err = client.Invoke(context.Background(), "GetBusinessObject", &consolev1.GetBusinessObjectRequest{}, &consolev1.GetBusinessObjectResponse{})
			if err == nil || errors.Is(err, ErrNotFound) {
				t.Fatalf("untrusted 404 was treated as deletion: %v", err)
			}
		})
	}
}

func TestClientRejectsBadAuthentication(t *testing.T) {
	fixture := newContractServer(t)
	server := httptest.NewServer(fixture)
	defer server.Close()
	client, err := NewClient(server.URL, "wrong-token", fixture.organizationID, fixture.workspaceID, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	err = client.Invoke(context.Background(), "GetBusinessObject", &consolev1.GetBusinessObjectRequest{
		OrganizationId: fixture.organizationID,
		WorkspaceId:    fixture.workspaceID,
		ObjectId:       "missing",
	}, &consolev1.GetBusinessObjectResponse{})
	var apiError *APIError
	if !errors.As(err, &apiError) || apiError.Code != "unauthenticated" {
		t.Fatalf("expected unauthenticated API error, got %v", err)
	}
}

func TestNewClientRejectsNonHTTPSEndpoint(t *testing.T) {
	if _, err := NewClient("file:///tmp/deixic", "token", "org", "workspace", nil); err == nil {
		t.Fatal("expected invalid endpoint error")
	}
	if _, err := NewClient("http://api.example.com", "token", "org", "workspace", nil); err == nil {
		t.Fatal("expected non-loopback plaintext endpoint error")
	}
	if _, err := NewClient("https://user:password@api.example.com", "token", "org", "workspace", nil); err == nil {
		t.Fatal("expected endpoint user information error")
	}
}

func TestClientRejectsRedirectWithoutFollowingIt(t *testing.T) {
	var targetCalls atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		targetCalls.Add(1)
	}))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	client, err := NewClient(redirect.URL, "token", "org", "workspace", redirect.Client())
	if err != nil {
		t.Fatal(err)
	}
	err = client.Invoke(context.Background(), "GetBusinessObject", &consolev1.GetBusinessObjectRequest{}, &consolev1.GetBusinessObjectResponse{})
	if err == nil {
		t.Fatal("expected redirect response error")
	}
	if targetCalls.Load() != 0 {
		t.Fatalf("redirect target received %d calls", targetCalls.Load())
	}
}
