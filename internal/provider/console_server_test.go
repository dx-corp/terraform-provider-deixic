package provider

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"

	consolev1 "github.com/dx-corp/terraform-provider-deixic/internal/consoleproto"
	"google.golang.org/protobuf/proto"
)

// consoleHandler answers one console RPC. Feature test files register theirs in init.
type consoleHandler func(s *consoleServer, body []byte) (proto.Message, *consoleFailure)

type consoleFailure struct {
	status  int
	code    string
	message string
}

var consoleHandlers = map[string]consoleHandler{}

// consoleServer is an in-memory stand-in for the console RPCs of the platform API.
type consoleServer struct {
	t              *testing.T
	mu             sync.Mutex
	token          string
	organizationID string
	workspaceID    string
	nextID         int
	types          []*consolev1.BusinessObjectType
	objects        map[string]*consolev1.BusinessObject
	relationships  map[string]*consolev1.BusinessObjectRelationship
	calls          map[string]int
}

func newConsoleServer(t *testing.T) *consoleServer {
	t.Helper()
	return &consoleServer{
		t:              t,
		token:          "test-token",
		organizationID: "org_test",
		workspaceID:    "org_test-default",
		objects:        map[string]*consolev1.BusinessObject{},
		relationships:  map[string]*consolev1.BusinessObjectRelationship{},
		calls:          map[string]int{},
	}
}

func (s *consoleServer) start(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(s)
	t.Cleanup(server.Close)
	return server
}

func (s *consoleServer) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	method := strings.TrimPrefix(request.URL.Path, consoleServicePath)
	handler, found := consoleHandlers[method]
	switch {
	case request.Method != http.MethodPost || !strings.HasPrefix(request.URL.Path, consoleServicePath) || !found:
		s.fail(writer, &consoleFailure{http.StatusNotFound, "not_found", "unknown RPC " + request.URL.Path})
		return
	case request.Header.Get("Authorization") != "Bearer "+s.token:
		s.fail(writer, &consoleFailure{http.StatusUnauthorized, "unauthenticated", "invalid bearer token"})
		return
	case request.Header.Get("X-Organization-Id") != s.organizationID || request.Header.Get("X-Workspace-Id") != s.workspaceID:
		s.fail(writer, &consoleFailure{http.StatusForbidden, "permission_denied", "tenant headers do not match"})
		return
	}
	body := make([]byte, request.ContentLength)
	if _, err := request.Body.Read(body); err != nil && err.Error() != "EOF" {
		s.t.Errorf("read body: %v", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls[method]++
	message, failure := handler(s, body)
	if failure != nil {
		s.fail(writer, failure)
		return
	}
	payload, err := proto.Marshal(message)
	if err != nil {
		s.t.Fatalf("marshal %s response: %v", method, err)
	}
	writer.Header().Set("Content-Type", "application/proto")
	writer.Header().Set("Connect-Protocol-Version", "1")
	_, _ = writer.Write(payload)
}

func (s *consoleServer) fail(writer http.ResponseWriter, failure *consoleFailure) {
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Connect-Protocol-Version", "1")
	writer.WriteHeader(failure.status)
	_, _ = fmt.Fprintf(writer, `{"code":%q,"message":%q}`, failure.code, failure.message)
}

func (s *consoleServer) addType(typeID, sourceTypeID string) {
	s.types = append(s.types, &consolev1.BusinessObjectType{
		TypeId:          typeID,
		Name:            sourceTypeID,
		BlueprintSource: &consolev1.BusinessBlueprintSource{SourceTypeId: sourceTypeID},
	})
}

func (s *consoleServer) addObject(typeID string, values ...*consolev1.BusinessFieldValue) {
	s.nextID++
	object := &consolev1.BusinessObject{
		ObjectId:       fmt.Sprintf("business_object_%03d", s.nextID),
		TypeId:         typeID,
		SchemaRevision: 1,
		Revision:       1,
		Values:         values,
	}
	s.objects[object.GetObjectId()] = object
}

func (s *consoleServer) objectsOfType(typeID string) []*consolev1.BusinessObject {
	var found []*consolev1.BusinessObject
	for _, object := range s.objects {
		if object.GetTypeId() == typeID && !object.GetDeleted() {
			found = append(found, object)
		}
	}
	sort.Slice(found, func(i, j int) bool { return found[i].GetObjectId() < found[j].GetObjectId() })
	return found
}

func decodeConsole(body []byte, request proto.Message) *consoleFailure {
	if err := proto.Unmarshal(body, request); err != nil {
		return &consoleFailure{http.StatusBadRequest, "invalid_argument", "invalid protobuf"}
	}
	return nil
}

func init() {
	consoleHandlers["ListBusinessObjectTypes"] = func(s *consoleServer, body []byte) (proto.Message, *consoleFailure) {
		request := &consolev1.ListBusinessObjectTypesRequest{}
		if failure := decodeConsole(body, request); failure != nil {
			return nil, failure
		}
		return &consolev1.ListBusinessObjectTypesResponse{Definitions: s.types}, nil
	}
	consoleHandlers["ListBusinessObjects"] = func(s *consoleServer, body []byte) (proto.Message, *consoleFailure) {
		request := &consolev1.ListBusinessObjectsRequest{}
		if failure := decodeConsole(body, request); failure != nil {
			return nil, failure
		}
		return &consolev1.ListBusinessObjectsResponse{Objects: s.objectsOfType(request.GetTypeId())}, nil
	}
}
