package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	consolev1 "github.com/dx-corp/terraform-provider-deixic/internal/consoleproto"
)

const consolePageSize = 100

// listObjectTypes returns every business object type definition in the workspace.
func (c *Client) listObjectTypes(ctx context.Context) ([]*consolev1.BusinessObjectType, error) {
	var types []*consolev1.BusinessObjectType
	after := ""
	for {
		response := &consolev1.ListBusinessObjectTypesResponse{}
		err := c.InvokeConsole(ctx, "ListBusinessObjectTypes", &consolev1.ListBusinessObjectTypesRequest{
			OrganizationId: c.organizationID,
			WorkspaceId:    c.workspaceID,
			Limit:          consolePageSize,
			AfterTypeId:    after,
		}, response)
		if err != nil {
			return nil, err
		}
		types = append(types, response.GetDefinitions()...)
		after = response.GetNextTypeId()
		if after == "" {
			return types, nil
		}
	}
}

// resolveTypeID returns explicit when set. Otherwise it finds the one cloned
// type whose blueprint member is sourceTypeID, and fails when none or several match.
func (c *Client) resolveTypeID(ctx context.Context, explicit, sourceTypeID string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	types, err := c.listObjectTypes(ctx)
	if err != nil {
		return "", err
	}
	var matches []string
	for _, definition := range types {
		if definition.GetBlueprintSource().GetSourceTypeId() == sourceTypeID {
			matches = append(matches, definition.GetTypeId())
		}
	}
	sort.Strings(matches)
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no %q type is installed in this workspace; apply the compliance library first or set type_id", sourceTypeID)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("%d types provide %q (%s); set type_id to choose one", len(matches), sourceTypeID, strings.Join(matches, ", "))
	}
}

// listObjects returns every record of one type.
func (c *Client) listObjects(ctx context.Context, typeID string) ([]*consolev1.BusinessObject, error) {
	var objects []*consolev1.BusinessObject
	after := ""
	for {
		response := &consolev1.ListBusinessObjectsResponse{}
		err := c.InvokeConsole(ctx, "ListBusinessObjects", &consolev1.ListBusinessObjectsRequest{
			OrganizationId: c.organizationID,
			WorkspaceId:    c.workspaceID,
			TypeId:         typeID,
			Limit:          consolePageSize,
			AfterObjectId:  after,
		}, response)
		if err != nil {
			return nil, err
		}
		for _, object := range response.GetObjects() {
			if !object.GetDeleted() {
				objects = append(objects, object)
			}
		}
		after = response.GetNextObjectId()
		if after == "" {
			return objects, nil
		}
	}
}

// fieldText returns a text or enum field of a record, or "" when absent.
func fieldText(object *consolev1.BusinessObject, fieldID string) string {
	for _, value := range object.GetValues() {
		if value.GetFieldId() != fieldID {
			continue
		}
		if text := value.GetText(); text != "" {
			return text
		}
		return value.GetEnumValue()
	}
	return ""
}

func textField(fieldID, text string) *consolev1.BusinessFieldValue {
	return &consolev1.BusinessFieldValue{FieldId: fieldID, Value: &consolev1.BusinessFieldValue_Text{Text: text}}
}

// stableKey derives a deterministic idempotency key from the parts, so a retried
// apply replays the original operation instead of creating a second record.
func stableKey(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return "tf-" + hex.EncodeToString(sum[:])[:40]
}

// listBlueprints returns the catalog, optionally limited to an ID prefix.
func (c *Client) listBlueprints(ctx context.Context, prefix string) ([]*consolev1.BusinessBlueprint, error) {
	response := &consolev1.ListBusinessBlueprintsResponse{}
	err := c.InvokeConsole(ctx, "ListBusinessBlueprints", &consolev1.ListBusinessBlueprintsRequest{
		OrganizationId: c.organizationID,
		WorkspaceId:    c.workspaceID,
	}, response)
	if err != nil {
		return nil, err
	}
	var kept []*consolev1.BusinessBlueprint
	for _, blueprint := range response.GetBlueprints() {
		if strings.HasPrefix(blueprint.GetBlueprintId(), prefix) {
			kept = append(kept, blueprint)
		}
	}
	return kept, nil
}
