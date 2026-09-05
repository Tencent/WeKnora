package file

import (
	"context"
	"testing"
)

func TestStorageClientsRejectUnsafeEndpointAtConstruction(t *testing.T) {
	const endpoint = "http://169.254.169.254/latest/meta-data"
	tests := map[string]func() error{
		"minio": func() error {
			_, err := newMinioClient(endpoint, "ak", "sk", "bucket", false)
			return err
		},
		"s3": func() error {
			_, err := newS3Client(endpoint, "ak", "sk", "bucket", "region", "", true)
			return err
		},
		"obs": func() error {
			return CheckObsConnectivity(context.Background(), endpoint, "region", "ak", "sk", "bucket")
		},
		"oss": func() error {
			_, err := newOSSClient(endpoint, "region", "ak", "sk", "")
			return err
		},
		"tos": func() error {
			return CheckTosConnectivity(context.Background(), endpoint, "region", "ak", "sk", "bucket")
		},
		"ks3": func() error {
			_, err := newKS3Client(endpoint, "region", "ak", "sk")
			return err
		},
	}
	for name, run := range tests {
		t.Run(name, func(t *testing.T) {
			if err := run(); err == nil {
				t.Fatal("expected unsafe endpoint to be rejected")
			}
		})
	}
}
