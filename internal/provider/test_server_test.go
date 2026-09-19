package provider

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"testing"

	consolev1 "buf.build/gen/go/evalops-infra/proto/protocolbuffers/go/console/v1"
	"google.golang.org/protobuf/proto"
)

type contractServer struct {
	t              *testing.T
	mu             sync.Mutex
	token          string
	organizationID string
	workspaceID    string
	nextID         int
	objects        map[string]*consolev1.BusinessObject
}

func newContractServer(t *testing.T) *contractServer {
	t.Helper()
	return &contractServer{
		t:              t,
		token:          "test-token",
		organizationID: "org/terraform.test",
		workspaceID:    "workspace:terraform.test",
		objects:        map[string]*consolev1.BusinessObject{},
	}
}

func (s *contractServer) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost || !strings.HasPrefix(request.URL.Path, deixicServicePath) {
		s.writeError(writer, http.StatusNotFound, "not_found", "unknown RPC")
		return
	}
	if request.Header.Get("Authorization") != "Bearer "+s.token {
		s.writeError(writer, http.StatusUnauthorized, "unauthenticated", "invalid bearer token")
		return
	}
	if request.Header.Get("Content-Type") != "application/proto" || request.Header.Get("Accept") != "application/proto" || request.Header.Get("Connect-Protocol-Version") != "1" {
		s.writeError(writer, http.StatusBadRequest, "invalid_argument", "binary Connect unary headers are required")
		return
	}
	if request.Header.Get("X-Organization-Id") != s.organizationID || request.Header.Get("X-Workspace-Id") != s.workspaceID {
		s.writeError(writer, http.StatusForbidden, "permission_denied", "tenant headers do not match")
		return
	}
	body, err := io.ReadAll(request.Body)
	if err != nil {
		s.t.Errorf("read request body: %v", err)
		s.writeError(writer, http.StatusBadRequest, "invalid_argument", "invalid body")
		return
	}
	method := strings.TrimPrefix(request.URL.Path, deixicServicePath)
	switch method {
	case "CreateBusinessObject":
		s.create(writer, body)
	case "GetBusinessObject":
		s.get(writer, body)
	case "UpdateBusinessObject":
		s.update(writer, body)
	case "DeleteBusinessObject":
		s.delete(writer, body)
	default:
		s.writeError(writer, http.StatusNotFound, "not_found", "unknown RPC")
	}
}

func (s *contractServer) create(writer http.ResponseWriter, body []byte) {
	request := &consolev1.CreateBusinessObjectRequest{}
	if !s.decodeRequest(writer, body, request) || !s.validScope(writer, request.GetOrganizationId(), request.GetWorkspaceId()) {
		return
	}
	if request.GetIdempotencyKey() == "" || request.GetTypeId() == "" || request.GetSchemaRevision() <= 0 {
		s.writeError(writer, http.StatusBadRequest, "invalid_argument", "create coordinates are required")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	object := &consolev1.BusinessObject{
		ObjectId:       fmt.Sprintf("business_object_test%03d", s.nextID),
		TypeId:         request.GetTypeId(),
		SchemaRevision: request.GetSchemaRevision(),
		Revision:       1,
		Values:         s.cloneValues(request.GetValues()),
		CreatedAt:      "2026-09-18T18:00:00Z",
		UpdatedAt:      "2026-09-18T18:00:00Z",
	}
	s.objects[object.GetObjectId()] = object
	s.writeProto(writer, &consolev1.CreateBusinessObjectResponse{Object: s.cloneObject(object)})
}

func (s *contractServer) get(writer http.ResponseWriter, body []byte) {
	request := &consolev1.GetBusinessObjectRequest{}
	if !s.decodeRequest(writer, body, request) || !s.validScope(writer, request.GetOrganizationId(), request.GetWorkspaceId()) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	object := s.objects[request.GetObjectId()]
	if object == nil {
		s.writeError(writer, http.StatusNotFound, "not_found", "Business object or definition not found")
		return
	}
	s.writeProto(writer, &consolev1.GetBusinessObjectResponse{Object: s.cloneObject(object)})
}

func (s *contractServer) update(writer http.ResponseWriter, body []byte) {
	request := &consolev1.UpdateBusinessObjectRequest{}
	if !s.decodeRequest(writer, body, request) || !s.validScope(writer, request.GetOrganizationId(), request.GetWorkspaceId()) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	object := s.objects[request.GetObjectId()]
	if object == nil {
		s.writeError(writer, http.StatusNotFound, "not_found", "Business object or definition not found")
		return
	}
	if object.GetDeleted() || request.GetExpectedRevision() != object.GetRevision() {
		s.writeError(writer, http.StatusConflict, "aborted", "Object revision changed")
		return
	}
	if request.GetIdempotencyKey() == "" || request.GetSchemaRevision() < object.GetSchemaRevision() {
		s.writeError(writer, http.StatusBadRequest, "invalid_argument", "invalid update")
		return
	}
	object.SchemaRevision = request.GetSchemaRevision()
	object.Revision++
	object.Values = s.cloneValues(request.GetValues())
	object.UpdatedAt = "2026-09-18T18:01:00Z"
	s.writeProto(writer, &consolev1.UpdateBusinessObjectResponse{Object: s.cloneObject(object)})
}

func (s *contractServer) delete(writer http.ResponseWriter, body []byte) {
	request := &consolev1.DeleteBusinessObjectRequest{}
	if !s.decodeRequest(writer, body, request) || !s.validScope(writer, request.GetOrganizationId(), request.GetWorkspaceId()) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	object := s.objects[request.GetObjectId()]
	if object == nil {
		s.writeError(writer, http.StatusNotFound, "not_found", "Business object or definition not found")
		return
	}
	if object.GetDeleted() || request.GetExpectedRevision() != object.GetRevision() {
		s.writeError(writer, http.StatusConflict, "aborted", "Object revision changed")
		return
	}
	if request.GetIdempotencyKey() == "" {
		s.writeError(writer, http.StatusBadRequest, "invalid_argument", "idempotency key is required")
		return
	}
	object.Deleted = true
	object.Revision++
	object.UpdatedAt = "2026-09-18T18:02:00Z"
	s.writeProto(writer, &consolev1.DeleteBusinessObjectResponse{Object: s.cloneObject(object)})
}

func (s *contractServer) decodeRequest(writer http.ResponseWriter, body []byte, request proto.Message) bool {
	if err := proto.Unmarshal(body, request); err != nil {
		s.writeError(writer, http.StatusBadRequest, "invalid_argument", "invalid protobuf")
		return false
	}
	return true
}

func (s *contractServer) validScope(writer http.ResponseWriter, organizationID, workspaceID string) bool {
	if organizationID != s.organizationID || workspaceID != s.workspaceID {
		s.writeError(writer, http.StatusForbidden, "permission_denied", "request tenant scope does not match")
		return false
	}
	return true
}

func (s *contractServer) writeProto(writer http.ResponseWriter, message proto.Message) {
	body, err := proto.Marshal(message)
	if err != nil {
		s.t.Fatalf("marshal fixture response: %v", err)
	}
	writer.Header().Set("Content-Type", "application/proto")
	writer.Header().Set("Connect-Protocol-Version", "1")
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(body)
}

func (s *contractServer) writeError(writer http.ResponseWriter, status int, code, message string) {
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Connect-Protocol-Version", "1")
	writer.Header().Set("X-Evalops-Error-Code", "business_object_"+code)
	writer.WriteHeader(status)
	if err := json.NewEncoder(writer).Encode(map[string]string{"code": code, "message": message}); err != nil {
		s.t.Fatalf("encode fixture error response: %v", err)
	}
}

func (s *contractServer) markDeleted(objectID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if object := s.objects[objectID]; object != nil {
		object.Deleted = true
		object.Revision++
	}
}

func (s *contractServer) allDeleted() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, object := range s.objects {
		if !object.GetDeleted() {
			return false
		}
	}
	return true
}

func (s *contractServer) cloneObject(object *consolev1.BusinessObject) *consolev1.BusinessObject {
	cloned, ok := proto.Clone(object).(*consolev1.BusinessObject)
	if !ok {
		s.t.Fatalf("clone fixture object returned %T", cloned)
	}
	return cloned
}

func (s *contractServer) cloneValues(values []*consolev1.BusinessFieldValue) []*consolev1.BusinessFieldValue {
	result := make([]*consolev1.BusinessFieldValue, 0, len(values))
	for _, value := range values {
		cloned, ok := proto.Clone(value).(*consolev1.BusinessFieldValue)
		if !ok {
			s.t.Fatalf("clone fixture field value returned %T", cloned)
		}
		result = append(result, cloned)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].GetFieldId() < result[j].GetFieldId() })
	return result
}
