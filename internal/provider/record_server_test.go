package provider

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"

	publicv1 "github.com/dx-corp/terraform-provider-deixic/internal/publicproto"
	"google.golang.org/protobuf/proto"
)

// recordHandler answers one public record RPC. Feature test files register theirs in init.
type recordHandler func(s *recordServer, body []byte) (proto.Message, *recordFailure)

type recordFailure struct {
	status  int
	code    string
	message string
}

var recordHandlers = map[string]recordHandler{}

// recordServer is an in-memory stand-in for the public record RPCs of the platform API.
type recordServer struct {
	t              *testing.T
	mu             sync.Mutex
	token          string
	organizationID string
	workspaceID    string
	nextID         int
	types          []*publicv1.BusinessObjectType
	objects        map[string]*publicv1.BusinessObject
	relationships  map[string]*publicv1.BusinessObjectRelationship
	calls          map[string]int
}

func newRecordServer(t *testing.T) *recordServer {
	t.Helper()
	return &recordServer{
		t:              t,
		token:          "test-token",
		organizationID: "org_test",
		workspaceID:    "org_test-default",
		objects:        map[string]*publicv1.BusinessObject{},
		relationships:  map[string]*publicv1.BusinessObjectRelationship{},
		calls:          map[string]int{},
	}
}

func (s *recordServer) start(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(s)
	t.Cleanup(server.Close)
	return server
}

func (s *recordServer) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	method := strings.TrimPrefix(request.URL.Path, deixicServicePath)
	handler, found := recordHandlers[method]
	switch {
	case request.Method != http.MethodPost || !strings.HasPrefix(request.URL.Path, deixicServicePath) || !found:
		s.fail(writer, &recordFailure{http.StatusNotFound, "not_found", "unknown RPC " + request.URL.Path})
		return
	case request.Header.Get("Authorization") != "Bearer "+s.token:
		s.fail(writer, &recordFailure{http.StatusUnauthorized, "unauthenticated", "invalid bearer token"})
		return
	case request.Header.Get("X-Organization-Id") != s.organizationID || request.Header.Get("X-Workspace-Id") != s.workspaceID:
		s.fail(writer, &recordFailure{http.StatusForbidden, "permission_denied", "tenant headers do not match"})
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

func (s *recordServer) fail(writer http.ResponseWriter, failure *recordFailure) {
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Connect-Protocol-Version", "1")
	writer.WriteHeader(failure.status)
	_, _ = fmt.Fprintf(writer, `{"code":%q,"message":%q}`, failure.code, failure.message)
}

func (s *recordServer) addType(typeID, sourceTypeID string) {
	s.types = append(s.types, &publicv1.BusinessObjectType{
		TypeId:          typeID,
		Name:            sourceTypeID,
		BlueprintOrigin: &publicv1.BusinessBlueprintOrigin{SourceTypeId: sourceTypeID},
	})
}

func (s *recordServer) addObject(typeID string, values ...*publicv1.BusinessFieldValue) {
	s.nextID++
	object := &publicv1.BusinessObject{
		ObjectId:       fmt.Sprintf("business_object_%03d", s.nextID),
		TypeId:         typeID,
		SchemaRevision: 1,
		Revision:       1,
		Values:         values,
	}
	s.objects[object.GetObjectId()] = object
}

func (s *recordServer) objectsOfType(typeID string) []*publicv1.BusinessObject {
	var found []*publicv1.BusinessObject
	for _, object := range s.objects {
		if object.GetTypeId() == typeID && !object.GetDeleted() {
			found = append(found, object)
		}
	}
	sort.Slice(found, func(i, j int) bool { return found[i].GetObjectId() < found[j].GetObjectId() })
	return found
}

func decodeRecord(body []byte, request proto.Message) *recordFailure {
	if err := proto.Unmarshal(body, request); err != nil {
		return &recordFailure{http.StatusBadRequest, "invalid_argument", "invalid protobuf"}
	}
	return nil
}

func init() {
	recordHandlers["ListBusinessObjectTypes"] = func(s *recordServer, body []byte) (proto.Message, *recordFailure) {
		request := &publicv1.ListBusinessObjectTypesRequest{}
		if failure := decodeRecord(body, request); failure != nil {
			return nil, failure
		}
		return &publicv1.ListBusinessObjectTypesResponse{ObjectTypes: s.types}, nil
	}
	recordHandlers["ListBusinessObjects"] = func(s *recordServer, body []byte) (proto.Message, *recordFailure) {
		request := &publicv1.ListBusinessObjectsRequest{}
		if failure := decodeRecord(body, request); failure != nil {
			return nil, failure
		}
		return &publicv1.ListBusinessObjectsResponse{Objects: s.objectsOfType(request.GetTypeId())}, nil
	}
}
