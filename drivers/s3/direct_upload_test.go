package s3

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
	awss3 "github.com/aws/aws-sdk-go/service/s3"
	"github.com/itsHenry35/gofakes3"
	"github.com/itsHenry35/gofakes3/s3mem"
)

func TestGetDirectUploadPartSize(t *testing.T) {
	const mib = int64(1024 * 1024)
	tests := []struct {
		name       string
		configured int64
		fileSize   int64
		want       int64
	}{
		{name: "minimum", configured: 0, fileSize: mib, want: 5 * mib},
		{name: "configured", configured: 16, fileSize: 32 * mib, want: 16 * mib},
		{name: "increase to stay within S3 part limit", configured: 5, fileSize: 50001 * mib, want: 6 * mib},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &S3{Addition: Addition{DirectUploadPartSize: tt.configured}}
			if got := d.getDirectUploadPartSize(tt.fileSize); got != tt.want {
				t.Fatalf("getDirectUploadPartSize(%d) = %d, want %d", tt.fileSize, got, tt.want)
			}
		})
	}
}

func TestMultipartDirectUpload(t *testing.T) {
	server := httptest.NewServer(gofakes3.New(s3mem.New()).Server())
	t.Cleanup(server.Close)

	ctx := context.Background()
	d := &S3{Addition: Addition{
		Bucket:               "direct-upload-test",
		Endpoint:             server.URL,
		Region:               "test",
		AccessKeyID:          "access-key",
		SecretAccessKey:      "secret-key",
		ForcePathStyle:       true,
		SignURLExpire:        1,
		EnableDirectUpload:   true,
		DirectUploadPartSize: 5,
	}}
	if err := d.Init(ctx); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if _, err := d.client.CreateBucketWithContext(ctx, &awss3.CreateBucketInput{Bucket: &d.Bucket}); err != nil {
		t.Fatalf("CreateBucketWithContext() error = %v", err)
	}

	const mib = int64(1024 * 1024)
	const fileSize = 6 * mib
	infoAny, err := d.GetDirectUploadInfo(ctx, "HttpDirect", &model.Object{Path: "/"}, "multipart.bin", fileSize)
	if err != nil {
		t.Fatalf("GetDirectUploadInfo() error = %v", err)
	}
	info, ok := infoAny.(*model.HttpDirectUploadInfo)
	if !ok || info.Multipart == nil {
		t.Fatalf("GetDirectUploadInfo() = %#v, want multipart info", infoAny)
	}

	partSizes := []int64{5 * mib, mib}
	for i, partSize := range partSizes {
		partInfo, err := d.GetDirectUploadPartInfo(ctx, &model.Object{Path: "/"}, "multipart.bin", info.Multipart.UploadID, int64(i+1))
		if err != nil {
			t.Fatalf("GetDirectUploadPartInfo(%d) error = %v", i+1, err)
		}
		req, err := http.NewRequestWithContext(ctx, partInfo.Method, partInfo.UploadURL, bytes.NewReader(make([]byte, partSize)))
		if err != nil {
			t.Fatalf("NewRequestWithContext() error = %v", err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("upload part %d error = %v", i+1, err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			t.Fatalf("upload part %d status = %d", i+1, resp.StatusCode)
		}
	}

	if err := d.CompleteDirectUpload(ctx, &model.Object{Path: "/"}, "multipart.bin", info.Multipart.UploadID); err != nil {
		t.Fatalf("CompleteDirectUpload() error = %v", err)
	}
	head, err := d.client.HeadObjectWithContext(ctx, &awss3.HeadObjectInput{Bucket: &d.Bucket, Key: awsString("multipart.bin")})
	if err != nil {
		t.Fatalf("HeadObjectWithContext() error = %v", err)
	}
	if head.ContentLength == nil || *head.ContentLength != fileSize {
		t.Fatalf("uploaded size = %v, want %d", head.ContentLength, fileSize)
	}
}

func awsString(value string) *string {
	return &value
}
