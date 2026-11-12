package types

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidSourceURL(t *testing.T) {
	for _, raw := range []string{
		"https://tenant.feishu.cn/wiki/Node?from=share#heading",
		"http://docs.example.test/document.pdf",
		"https://docs.example.test/a%20document?q=hello+world",
	} {
		require.Equal(t, raw, ValidSourceURL("  "+raw+"  "))
	}
	for _, raw := range []string{
		"", "manual", "//docs.example.test/doc", "/documents/one", "javascript:alert(1)",
		"resource://resource-id", "s3://bucket/object", "file:///tmp/document.pdf", "https:///doc",
		"https://user:password@docs.example.test/doc", "https://user@docs.example.test/doc",
		"https://docs.example.test/doc\nsecret", "https://docs.example.test/doc?bad=%zz",
		"https://files.example.test/api/v1/files/presigned?path=object",
		"https://files.example.test/api/v1/resources/object", "https://files.example.test/r/access-grant",
		"https://open.feishu.cn/open-apis/drive/v1/files/file/download",
	} {
		t.Run(raw, func(t *testing.T) { require.Empty(t, ValidSourceURL(raw)) })
	}
	for _, key := range []string{
		"X-Amz-Signature",
		"X-Goog-Credential",
		"Signature",
		"sIg",
		"sign",
		"q-signature",
		"token",
		"access_token",
		"download_token",
		"client_secret", "auth_key", "Authorization", "AWSAccessKeyId", "OSSAccessKeyId", "x-oss-security-token",
		"x-cos-security-token", "Policy", "Key-Pair-Id", "Expires",
	} {
		t.Run(key, func(t *testing.T) {
			require.Empty(t, ValidSourceURL("https://files.example.test/doc?"+key+"=credential"))
		})
	}
}
