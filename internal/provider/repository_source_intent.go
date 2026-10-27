package provider

import (
	"context"
	"errors"
	"fmt"
	"sort"

	publicv1 "github.com/dx-corp/terraform-provider-deixic/internal/publicproto"
)

type repositorySourceIntent struct {
	definition *publicv1.BusinessObjectType
	groups     []string
	fields     []*publicv1.BusinessSourceFieldMapping
}

// repositoryBindingIntent validates caller-selected references against the
// current owner schema before creating any record. The owner still admits the bind.
func (c *Client) repositoryBindingIntent(ctx context.Context, typeID string, groups []string, mappings map[string]string) (*repositorySourceIntent, error) {
	if typeID == "" {
		return nil, errors.New("repository_type_id must name an existing source-bindable type")
	}
	if len(groups) == 0 || len(groups) > 64 {
		return nil, errors.New("source_group_ids must select between 1 and 64 groups")
	}
	if len(mappings) > 64 {
		return nil, errors.New("source_fields must contain at most 64 mappings")
	}
	definitions, err := c.listObjectTypes(ctx)
	if err != nil {
		return nil, err
	}
	var definition *publicv1.BusinessObjectType
	for _, candidate := range definitions {
		if candidate.GetTypeId() == typeID && (definition == nil || candidate.GetRevision() > definition.GetRevision()) {
			definition = candidate
		}
	}
	if definition == nil {
		return nil, fmt.Errorf("repository type %q is not visible or does not exist", typeID)
	}
	if definition.GetRevision() <= 0 {
		return nil, fmt.Errorf("repository type %q has no published revision", typeID)
	}
	fields := map[string]*publicv1.BusinessObjectTypeField{}
	knownGroups := map[string][]*publicv1.BusinessObjectTypeField{}
	for _, field := range definition.GetFields() {
		fields[field.GetFieldId()] = field
		knownGroups[field.GetGroupId()] = append(knownGroups[field.GetGroupId()], field)
	}
	display := fields[definition.GetDisplayFieldId()]
	if display == nil || !display.GetSourceMappable() {
		return nil, errors.New("repository type must declare a text display field")
	}
	selected := map[string]bool{}
	for _, group := range groups {
		if selected[group] {
			return nil, fmt.Errorf("source group %q is selected more than once", group)
		}
		if group == "" || len(knownGroups[group]) == 0 {
			return nil, fmt.Errorf("unknown source group %q in repository type %q", group, typeID)
		}
		selected[group] = true
		for _, field := range knownGroups[group] {
			if !field.GetSourceMappable() {
				return nil, fmt.Errorf("source group %q contains field %q that cannot be source-mapped; select an existing type with text-only source groups", group, field.GetFieldId())
			}
			if _, ok := mappings[field.GetFieldId()]; !ok {
				return nil, fmt.Errorf("source group %q requires a mapping for field %q", group, field.GetFieldId())
			}
		}
	}
	intent := &repositorySourceIntent{definition: definition, groups: append([]string(nil), groups...)}
	sort.Strings(intent.groups)
	names := make([]string, 0, len(mappings))
	for name := range mappings {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		field := fields[name]
		if field == nil {
			return nil, fmt.Errorf("unknown source mapping field %q", name)
		}
		if !selected[field.GetGroupId()] {
			return nil, fmt.Errorf("source mapping field %q is outside the selected groups", name)
		}
		var property publicv1.BusinessSourceFieldProperty
		switch mappings[name] {
		case "display_name":
			property = publicv1.BusinessSourceFieldProperty_BUSINESS_SOURCE_FIELD_PROPERTY_DISPLAY_NAME
		case "external_id":
			property = publicv1.BusinessSourceFieldProperty_BUSINESS_SOURCE_FIELD_PROPERTY_EXTERNAL_ID
		default:
			return nil, fmt.Errorf("unsupported source property %q for field %q; use display_name or external_id", mappings[name], name)
		}
		intent.fields = append(intent.fields, &publicv1.BusinessSourceFieldMapping{FieldId: name, Property: property})
	}
	return intent, nil
}
