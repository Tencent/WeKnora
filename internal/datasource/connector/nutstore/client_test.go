package nutstore

import (
	"testing"
)

func TestParseResponse_RootDirSelfReference(t *testing.T) {
	// When basePath is "/", the self-referencing "/" entry must be skipped
	client := &Client{}

	r := response{
		Href: "/dav/",
		Propstat: propstat{
			Prop: prop{
				DisplayName:  "",
				ResourceType: resourceType{Collection: &struct{}{}},
			},
		},
	}

	fi := client.parseResponse(r, "/")
	if fi != nil {
		t.Errorf("expected root self-reference to be skipped, got %+v", fi)
	}
}

func TestParseResponse_NonRootDirSelfReference(t *testing.T) {
	client := &Client{}

	r := response{
		Href: "/dav/sub1/",
		Propstat: propstat{
			Prop: prop{
				DisplayName:  "sub1",
				ResourceType: resourceType{Collection: &struct{}{}},
			},
		},
	}

	fi := client.parseResponse(r, "/sub1/")
	if fi != nil {
		t.Errorf("expected dir self-reference to be skipped, got %+v", fi)
	}
}
