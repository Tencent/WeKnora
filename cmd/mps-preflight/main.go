// Command mps-preflight validates Tencent MPS access and discovers COS output buckets.
// It performs read-only API calls and never prints credentials.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
	mpssdk "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/mps/v20190612"
	cos "github.com/tencentyun/cos-go-sdk-v5"
)

func main() {
	secretID := strings.TrimSpace(os.Getenv("TENCENTCLOUD_SECRET_ID"))
	secretKey := strings.TrimSpace(os.Getenv("TENCENTCLOUD_SECRET_KEY"))
	region := strings.TrimSpace(os.Getenv("TENCENTCLOUD_REGION"))
	if region == "" {
		region = "ap-guangzhou"
	}
	if secretID == "" || secretKey == "" {
		fmt.Fprintln(os.Stderr, "missing TENCENTCLOUD_SECRET_ID or TENCENTCLOUD_SECRET_KEY")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := checkMPS(ctx, secretID, secretKey, region); err != nil {
		fmt.Fprintf(os.Stderr, "MPS check failed: %v\n", err)
		os.Exit(1)
	}
	buckets, err := listBuckets(ctx, secretID, secretKey)
	if err != nil {
		fmt.Fprintf(os.Stderr, "COS check failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("MPS access: ok (region=%s)\n", region)
	if len(buckets) == 0 {
		fmt.Println("COS buckets: none")
		return
	}
	fmt.Println("COS buckets:")
	for _, bucket := range buckets {
		fmt.Printf("- %s (%s)\n", bucket.Name, bucket.Region)
	}
}

func checkMPS(ctx context.Context, secretID, secretKey, region string) error {
	clientProfile := profile.NewClientProfile()
	clientProfile.HttpProfile.Endpoint = "mps.tencentcloudapi.com"
	client, err := mpssdk.NewClient(common.NewCredential(secretID, secretKey), region, clientProfile)
	if err != nil {
		return err
	}
	request := mpssdk.NewDescribeTasksRequest()
	request.Limit = common.Uint64Ptr(1)
	request.Status = common.StringPtr("FINISH")
	_, err = client.DescribeTasksWithContext(ctx, request)
	return err
}

func listBuckets(ctx context.Context, secretID, secretKey string) ([]cos.Bucket, error) {
	httpClient := &http.Client{Transport: &cos.AuthorizationTransport{SecretID: secretID, SecretKey: secretKey, Transport: &http.Transport{Proxy: http.ProxyFromEnvironment}}}
	client := cos.NewClient(nil, httpClient)
	result, _, err := client.Service.Get(ctx)
	if err != nil {
		return nil, err
	}
	return result.Buckets, nil
}
