package catalog

import "reflect"

// Snapshot retains a registry generation, including its built-in baseline.
// Its maps are private; callers cannot mutate a saved generation.
type Snapshot struct {
	current map[string]*Vendor
	base    map[string]*Vendor
}

// SnapshotCurrent captures the current definitions and their built-in baseline.
func SnapshotCurrent() Snapshot {
	mu.RLock()
	defer mu.RUnlock()
	return Snapshot{cloneVendors(vendors), cloneVendors(builtins)}
}

// RestoreSnapshot is intended for isolated registry tests and configuration rollback.
func RestoreSnapshot(s Snapshot) {
	mu.Lock()
	defer mu.Unlock()
	vendors, builtins = cloneVendors(s.current), cloneVendors(s.base)
}

func cloneVendors(in map[string]*Vendor) map[string]*Vendor {
	out := make(map[string]*Vendor, len(in))
	for id, v := range in {
		out[id] = cloneValue(reflect.ValueOf(v)).Interface().(*Vendor)
	}
	return out
}

// Definitions contain only exported data fields and immutable function values.
// Copy every pointer, map and slice, including nested compatibility settings.
func cloneValue(v reflect.Value) reflect.Value {
	switch v.Kind() {
	case reflect.Interface:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		out := reflect.New(v.Type()).Elem()
		out.Set(cloneValue(v.Elem()))
		return out
	case reflect.Pointer:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		out := reflect.New(v.Type().Elem())
		out.Elem().Set(cloneValue(v.Elem()))
		return out
	case reflect.Map:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		out := reflect.MakeMapWithSize(v.Type(), v.Len())
		iter := v.MapRange()
		for iter.Next() {
			out.SetMapIndex(iter.Key(), cloneValue(iter.Value()))
		}
		return out
	case reflect.Slice:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		out := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := 0; i < v.Len(); i++ {
			out.Index(i).Set(cloneValue(v.Index(i)))
		}
		return out
	case reflect.Struct:
		out := reflect.New(v.Type()).Elem()
		for i := 0; i < v.NumField(); i++ {
			out.Field(i).Set(cloneValue(v.Field(i)))
		}
		return out
	default:
		return v
	}
}
