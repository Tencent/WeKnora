package runtime

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/Tencent/WeKnora/internal/models/internal/configcopy"
)

// ApplyCompat writes the set fields of an overlay struct onto a settings struct
// with matching field names, dereferencing pointers. Slices and maps
// replace the default wholesale (no element merge) except map[string]any
// ExtraBody-style fields, which merge key by key.
func ApplyCompat(settings any, compat any) {
	sv := reflect.ValueOf(settings).Elem()
	cv := reflect.ValueOf(compat).Elem()
	ct := cv.Type()
	for i := 0; i < cv.NumField(); i++ {
		field := ct.Field(i)
		if !field.IsExported() {
			continue
		}
		target := sv.FieldByName(field.Name)
		if !target.IsValid() {
			panic(fmt.Sprintf("catalog: overlay field %s.%s has no settings counterpart", ct.Name(), field.Name))
		}
		f := cv.Field(i)
		switch f.Kind() {
		case reflect.Pointer:
			if f.IsNil() {
				continue
			}
			if target.Kind() == reflect.Pointer {
				target.Set(configcopy.Value(f))
			} else {
				target.Set(configcopy.Value(f.Elem()))
			}
		case reflect.Slice:
			if f.Len() > 0 {
				target.Set(configcopy.Value(f))
			}
		case reflect.Map:
			if f.Len() == 0 {
				continue
			}
			if target.IsNil() {
				target.Set(reflect.MakeMap(target.Type()))
			}
			iter := f.MapRange()
			for iter.Next() {
				target.SetMapIndex(iter.Key(), configcopy.Value(iter.Value()))
			}
		}
	}
}

// DecodeCompat unmarshals a flat compat object into the overlay type for an
// API. Unknown keys are rejected so typos in models.json surface early.
func DecodeCompat(raw json.RawMessage, into any) error {
	if len(raw) == 0 {
		return nil
	}
	dec := json.NewDecoder(bytesReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		return fmt.Errorf("decode compat: %w", err)
	}
	return nil
}

func bytesReader(b []byte) *bytes.Reader { return bytes.NewReader(b) }
