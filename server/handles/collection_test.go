package handles

import "testing"

func TestCollectionFileName(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  string
	}{
		{input: "report.pdf", want: "report.pdf"},
		{input: "folder%2Fphoto.jpg", want: "folder/photo.jpg"},
	} {
		got, err := collectionFileName(tc.input)
		if err != nil {
			t.Fatalf("collectionFileName(%q): %v", tc.input, err)
		}
		if got != tc.want {
			t.Fatalf("collectionFileName(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
	if _, err := collectionFileName("../secret.txt"); err == nil {
		t.Fatal("collectionFileName accepted parent traversal")
	}
}

func TestCollectionUploadTokenDataBindsRequest(t *testing.T) {
	base := collectionUploadTokenData("collect-1", "session-1", "report.pdf", 42, "upload-1")
	for _, changed := range []string{
		collectionUploadTokenData("collect-2", "session-1", "report.pdf", 42, "upload-1"),
		collectionUploadTokenData("collect-1", "session-2", "report.pdf", 42, "upload-1"),
		collectionUploadTokenData("collect-1", "session-1", "other.pdf", 42, "upload-1"),
		collectionUploadTokenData("collect-1", "session-1", "report.pdf", 43, "upload-1"),
		collectionUploadTokenData("collect-1", "session-1", "report.pdf", 42, "upload-2"),
	} {
		if changed == base {
			t.Fatal("upload token data did not change with a bound field")
		}
	}
}

func TestCollectionIDFromPath(t *testing.T) {
	id, err := collectionIDFromPath("/@c/my-collection")
	if err != nil || id != "my-collection" {
		t.Fatalf("collectionIDFromPath() = %q, %v", id, err)
	}
	if _, err := collectionIDFromPath("/@c/my-collection/file"); err == nil {
		t.Fatal("collectionIDFromPath accepted a nested browse path")
	}
}
