package handler

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// TestUpdateKBRequest_DoesNotAcceptVectorStoreID is the structural enforcement
// behind the vector_store_id immutability contract. The GORM `<-:create`
// tag on KnowledgeBase.VectorStoreID already blocks every ORM UPDATE path
// (verified by the repository-level sqlite immutability tests), but the
// service DTO must independently refuse to even *accept* the field —
// otherwise a future maintainer who adds it to UpdateKnowledgeBaseRequest
// or KnowledgeBaseConfig opens a path where the field is silently ignored
// by the ORM, which is worse than an explicit rejection.
//
// This test walks the request and config struct shapes and fails if either
// gains a VectorStoreID member, by name or by JSON tag.
func TestUpdateKBRequest_DoesNotAcceptVectorStoreID(t *testing.T) {
	t.Run("UpdateKnowledgeBaseRequest", func(t *testing.T) {
		assertNoVectorStoreIDField(t, reflect.TypeOf(UpdateKnowledgeBaseRequest{}))
	})
	t.Run("KnowledgeBaseConfig", func(t *testing.T) {
		// Config carries chunking / extract / faq / wiki sub-configs and must
		// not be extended with a VectorStoreID either (Config is passed
		// straight into the service Update path).
		assertNoVectorStoreIDField(t, reflect.TypeOf(types.KnowledgeBaseConfig{}))
	})
}

// assertNoVectorStoreIDField walks the visible fields of t (including embedded
// anonymous structs) and reports any field named VectorStoreID or carrying
// a json tag of "vector_store_id".
func assertNoVectorStoreIDField(t *testing.T, typ reflect.Type) {
	t.Helper()
	var visit func(rt reflect.Type, path string)
	visit = func(rt reflect.Type, path string) {
		for rt.Kind() == reflect.Ptr {
			rt = rt.Elem()
		}
		if rt.Kind() != reflect.Struct {
			return
		}
		for i := 0; i < rt.NumField(); i++ {
			f := rt.Field(i)
			full := path + "." + f.Name
			if f.Name == "VectorStoreID" {
				t.Fatalf("%s declares VectorStoreID — vector_store_id is immutable post-create "+
					"and must not be accepted by update DTOs", full)
			}
			if tag := strings.Split(f.Tag.Get("json"), ",")[0]; tag == "vector_store_id" {
				t.Fatalf("%s carries json tag \"vector_store_id\" — the field is immutable "+
					"post-create and must not be accepted by update DTOs", full)
			}
			if f.Anonymous {
				visit(f.Type, full)
			}
		}
	}
	visit(typ, typ.Name())
}
