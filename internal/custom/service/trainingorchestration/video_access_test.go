package trainingorchestration

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/custom/model"
)

type fakeObjectExistence struct {
	exists bool
	err    error
	key    string
}

func (f *fakeObjectExistence) ObjectExists(_ context.Context, key string) (bool, error) {
	f.key = key
	return f.exists, f.err
}

func TestDefaultVideoAccessReaderUsesStoredObjectKey(t *testing.T) {
	objects := &fakeObjectExistence{exists: true}
	reader := NewVideoAccessReader(objects)
	result, err := reader.CheckAccessible(context.Background(), []model.Video{{
		ID: "video-1", UploadObjectKey: "videos/video-1/source.mp4", FileURL: "not-a-url",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !result["video-1"] || objects.key != "videos/video-1/source.mp4" {
		t.Fatalf("unexpected object access result: result=%#v key=%q", result, objects.key)
	}
}

func TestDefaultVideoAccessReaderResolvesObjectKeyFromPublicFileURL(t *testing.T) {
	objects := &fakeObjectExistence{exists: true}
	reader := NewVideoAccessReader(objects)
	result, err := reader.CheckAccessible(context.Background(), []model.Video{{
		ID: "video-1", FileURL: "http://localhost/api/custom/files/videos/video-1/source%20clip.mp4",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !result["video-1"] || objects.key != "videos/video-1/source clip.mp4" {
		t.Fatalf("unexpected object access result: result=%#v key=%q", result, objects.key)
	}
}

func TestDefaultVideoAccessReaderReadsByteWithRangeRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.Header.Get("Range") != "bytes=0-0" {
			t.Errorf("unexpected access request: method=%s range=%q", request.Method, request.Header.Get("Range"))
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		writer.Header().Set("Content-Length", "1")
		writer.WriteHeader(http.StatusPartialContent)
		_, _ = writer.Write([]byte{0})
	}))
	defer server.Close()

	reader := NewVideoAccessReader(nil)
	result, err := reader.CheckAccessible(context.Background(), []model.Video{{ID: "video-1", FileURL: server.URL + "/video.mp4"}})
	if err != nil {
		t.Fatal(err)
	}
	if !result["video-1"] {
		t.Fatalf("range-readable video was reported inaccessible: %#v", result)
	}
}

func TestDefaultVideoAccessReaderRejectsSuccessfulResponseWithoutBytes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	reader := NewVideoAccessReader(nil)
	result, err := reader.CheckAccessible(context.Background(), []model.Video{{ID: "video-1", FileURL: server.URL + "/empty.mp4"}})
	if err != nil {
		t.Fatal(err)
	}
	if result["video-1"] {
		t.Fatalf("empty successful response was reported accessible: %#v", result)
	}
}

func TestDefaultVideoAccessReaderDistinguishesMissingAndBackendFailure(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		objects := &fakeObjectExistence{exists: false}
		result, err := NewVideoAccessReader(objects).CheckAccessible(context.Background(), []model.Video{{ID: "video-1", UploadObjectKey: "missing.mp4"}})
		if err != nil || result["video-1"] {
			t.Fatalf("missing object result=%#v err=%v", result, err)
		}
	})

	t.Run("backend failure", func(t *testing.T) {
		objects := &fakeObjectExistence{err: errors.New("storage unavailable")}
		if _, err := NewVideoAccessReader(objects).CheckAccessible(context.Background(), []model.Video{{ID: "video-1", UploadObjectKey: "video.mp4"}}); err == nil {
			t.Fatal("storage failure was treated as an inaccessible video")
		}
	})
}
