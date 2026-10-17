package docparser

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/docreader/proto"
	"github.com/Tencent/WeKnora/internal/types"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type previewConvertServer struct {
	proto.UnimplementedDocReaderServer
	got  *proto.ConvertPreviewRequest
	resp *proto.ConvertPreviewResponse
}

func (s *previewConvertServer) ConvertPreview(
	_ context.Context, req *proto.ConvertPreviewRequest,
) (*proto.ConvertPreviewResponse, error) {
	s.got = req
	return s.resp, nil
}

func TestGRPCDocumentReaderConvertPreview(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := grpc.NewServer()
	stub := &previewConvertServer{resp: &proto.ConvertPreviewResponse{
		FileContent:   []byte("converted"),
		Converted:     true,
		SkippedReason: "",
		Warnings:      []string{"one image kept"},
	}}
	proto.RegisterDocReaderServer(server, stub)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	conn, err := grpc.NewClient(
		listener.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	reader := &GRPCDocumentReader{client: proto.NewDocReaderClient(conn)}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := reader.ConvertPreview(ctx, &types.ConvertPreviewRequest{
		FileContent: []byte("source"),
		FileName:    "manual.docx",
		FileType:    "docx",
		RequestID:   "req-1",
	})
	if err != nil {
		t.Fatalf("ConvertPreview: %v", err)
	}
	if string(result.FileContent) != "converted" || !result.Converted {
		t.Fatalf("unexpected result: %+v", result)
	}
	if len(result.Warnings) != 1 || result.Warnings[0] != "one image kept" {
		t.Fatalf("warnings = %v", result.Warnings)
	}
	if stub.got == nil {
		t.Fatal("server did not receive the request")
	}
	if string(stub.got.GetFileContent()) != "source" ||
		stub.got.GetFileName() != "manual.docx" ||
		stub.got.GetFileType() != "docx" ||
		stub.got.GetRequestId() != "req-1" {
		t.Fatalf("unexpected request: %+v", stub.got)
	}
}
