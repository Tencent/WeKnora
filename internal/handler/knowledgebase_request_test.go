package handler

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestFeedbackWeightOptInUpdateRequiresWebAdminOrOwner(t *testing.T) {
	current := types.IndexingStrategy{VectorEnabled: true}
	requested := &types.IndexingStrategy{VectorEnabled: true, FeedbackWeightEnabled: true}

	for _, testCase := range []struct {
		name          string
		role          types.TenantRole
		principalType string
		allowed       bool
	}{
		{name: "viewer", role: types.TenantRoleViewer, principalType: types.PrincipalWebUser},
		{name: "contributor", role: types.TenantRoleContributor, principalType: types.PrincipalWebUser},
		{name: "api key", role: types.TenantRoleOwner, principalType: types.PrincipalAPITenant},
		{name: "admin", role: types.TenantRoleAdmin, principalType: types.PrincipalWebUser, allowed: true},
		{name: "owner", role: types.TenantRoleOwner, principalType: types.PrincipalWebUser, allowed: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := types.WithPrincipal(
				context.WithValue(context.Background(), types.TenantRoleContextKey, testCase.role),
				types.Principal{Type: testCase.principalType, ID: "caller"},
			)
			if got := feedbackWeightUpdateAllowed(ctx, current, requested); got != testCase.allowed {
				t.Fatalf("allowed = %v, want %v", got, testCase.allowed)
			}
		})
	}

	if !feedbackWeightUpdateAllowed(context.Background(), current, &current) {
		t.Fatal("an unchanged opt-in value must not block unrelated KB updates")
	}
	if feedbackWeightUpdateAllowed(
		feedbackGovernanceRequestContext(types.TenantRoleContributor, types.PrincipalWebUser),
		types.IndexingStrategy{},
		&types.IndexingStrategy{FeedbackWeightEnabled: true},
	) {
		t.Fatal("a contributor must not enable feedback weighting while creating a KB")
	}
}

func feedbackGovernanceRequestContext(role types.TenantRole, principalType string) context.Context {
	return types.WithPrincipal(
		context.WithValue(context.Background(), types.TenantRoleContextKey, role),
		types.Principal{Type: principalType, ID: "caller"},
	)
}

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
