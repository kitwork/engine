package kitdb

import (
	"encoding/json"
	"fmt"
)

// Validate the final catalog graph, after all operations in a frame. This
// prevents independent frontends from dropping a still-referenced sequence or
// leaving an owned sequence behind when its table/field disappears.
func (state catalogState) validateSequenceReferences() error {
	type reference struct{ ID, Name, Mode string }
	type field struct {
		Tag      uint32
		Kind     string
		Sequence *reference
	}
	type definition struct{ Fields []field }
	owners := make(map[string]bool)
	for _, entry := range state.byID {
		if entry.Version < 3 {
			continue
		}
		var schema definition
		if err := json.Unmarshal(entry.Definition, &schema); err != nil {
			return err
		}
		for _, field := range schema.Fields {
			if field.Sequence == nil {
				continue
			}
			ref := field.Sequence
			sequence, found := state.sequences[ref.ID]
			if !found || sequence.Name != ref.Name {
				return fmt.Errorf("%w: field tag %d on %q references a missing sequence", ErrInvalidCatalog, field.Tag, entry.Name)
			}
			expected := map[string]string{"smallint": "smallint", "integer": "int32", "bigint": "bigint"}[sequence.DataTypeName()]
			if expected == "" || field.Kind != expected {
				return fmt.Errorf("%w: field tag %d on %q and sequence %q use different integer types", ErrInvalidCatalog, field.Tag, entry.Name, sequence.Name)
			}
			if ref.Mode != "default" {
				if ref.Mode != "serial" && ref.Mode != "always" && ref.Mode != "by_default" || sequence.OwnerStruct != entry.ID || sequence.OwnerTag != field.Tag {
					return fmt.Errorf("%w: identity sequence owner mismatch", ErrInvalidCatalog)
				}
				owners[sequence.ID] = true
			}
		}
	}
	for _, sequence := range state.sequences {
		if sequence.OwnerStruct != "" && !owners[sequence.ID] {
			return fmt.Errorf("%w: owned sequence %q has no owner field", ErrInvalidCatalog, sequence.Name)
		}
	}
	return nil
}
