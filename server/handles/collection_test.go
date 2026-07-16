package handles

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/gin-gonic/gin"
)

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

func TestCollectionPathFromPath(t *testing.T) {
	for _, tc := range []struct {
		path         string
		wantID       string
		wantRelative string
	}{
		{path: "/@c/collect-1", wantID: "collect-1"},
		{path: "/@c/collect-1/folder/report.pdf", wantID: "collect-1", wantRelative: "folder/report.pdf"},
		{path: "/@c/collect-1/folder%2Fphoto.jpg", wantID: "collect-1", wantRelative: "folder/photo.jpg"},
	} {
		id, relative, err := collectionPathFromPath(tc.path)
		if err != nil {
			t.Fatalf("collectionPathFromPath(%q): %v", tc.path, err)
		}
		if id != tc.wantID || relative != tc.wantRelative {
			t.Fatalf("collectionPathFromPath(%q) = %q, %q; want %q, %q", tc.path, id, relative, tc.wantID, tc.wantRelative)
		}
	}
	if _, _, err := collectionPathFromPath("/@c/collect-1/../secret.txt"); err == nil {
		t.Fatal("collectionPathFromPath accepted parent traversal")
	}
}

func TestCollectionChildrenBuildVirtualTree(t *testing.T) {
	first := time.Date(2026, 7, 17, 1, 2, 3, 0, time.UTC)
	second := first.Add(time.Minute)
	uploads := []model.CollectionUpload{
		{FileName: "root.txt", FileSize: 10, CompletedAt: &first},
		{FileName: "folder/a.txt", FileSize: 20, CompletedAt: &first},
		{FileName: "folder/nested/b.txt", FileSize: 30, CompletedAt: &second},
	}

	root, exists := collectionChildren(uploads, "")
	if !exists || len(root) != 2 {
		t.Fatalf("root children = %#v, exists=%v", root, exists)
	}
	if !root[0].IsDir || root[0].Name != "folder" || root[1].Name != "root.txt" || root[1].Size != 10 {
		t.Fatalf("unexpected root children: %#v", root)
	}
	folder, exists := collectionChildren(uploads, "folder")
	if !exists || len(folder) != 2 || !folder[0].IsDir || folder[0].Name != "nested" || folder[1].Name != "a.txt" {
		t.Fatalf("unexpected folder children: %#v, exists=%v", folder, exists)
	}
	if _, exists := collectionChildren(uploads, "other"); exists {
		t.Fatal("unowned virtual folder unexpectedly exists")
	}
}

func TestCollectionVisitorCookieIsStable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	firstRecorder := httptest.NewRecorder()
	firstContext, _ := gin.CreateTestContext(firstRecorder)
	firstContext.Request = httptest.NewRequest("POST", "/api/fs/list", nil)
	firstContext.Request.Header.Set("X-Forwarded-Proto", "https")
	firstHash := collectionVisitorHash(firstContext)
	cookies := firstRecorder.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("visitor cookies = %d, want 1", len(cookies))
	}
	cookie := cookies[0]
	if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("unexpected visitor cookie flags: %#v", cookie)
	}

	secondRecorder := httptest.NewRecorder()
	secondContext, _ := gin.CreateTestContext(secondRecorder)
	secondContext.Request = httptest.NewRequest("POST", "/api/fs/list", nil)
	secondContext.Request.AddCookie(cookie)
	secondHash := collectionVisitorHash(secondContext)
	if firstHash != secondHash {
		t.Fatal("visitor hash changed after returning the cookie")
	}
	if len(secondRecorder.Result().Cookies()) != 0 {
		t.Fatal("existing valid visitor cookie was replaced")
	}
}

func TestParseCollectionFields(t *testing.T) {
	fields, err := parseCollectionFields("* 姓名\n学号\n\n备注")
	if err != nil {
		t.Fatalf("parseCollectionFields(): %v", err)
	}
	if len(fields) != 3 || fields[0].Name != "姓名" || !fields[0].Required || fields[1].Required {
		t.Fatalf("unexpected fields: %#v", fields)
	}
	if _, err := parseCollectionFields("姓名\n姓名"); err == nil {
		t.Fatal("parseCollectionFields accepted duplicate names")
	}
	if _, err := parseCollectionFields("*"); err == nil {
		t.Fatal("parseCollectionFields accepted an empty required name")
	}
}

func TestValidateCollectionValues(t *testing.T) {
	fields := []model.CollectionField{
		{Name: "姓名", Required: true},
		{Name: "备注"},
	}
	values, err := validateCollectionValues(fields, map[string]string{"姓名": " Alice ", "ignored": "secret"})
	if err != nil {
		t.Fatalf("validateCollectionValues(): %v", err)
	}
	if values["姓名"] != "Alice" || values["备注"] != "" {
		t.Fatalf("unexpected normalized values: %#v", values)
	}
	if _, ok := values["ignored"]; ok {
		t.Fatal("unknown collection field was persisted")
	}
	if _, err := validateCollectionValues(fields, map[string]string{}); err == nil {
		t.Fatal("validateCollectionValues accepted a missing required value")
	}
}

func TestCollectionSubmissionFolderIsScoped(t *testing.T) {
	first := collectionSubmissionFolder("collect-1", "visitor-hash")
	if first != collectionSubmissionFolder("collect-1", "visitor-hash") {
		t.Fatal("collection submission folder is not stable")
	}
	if first == collectionSubmissionFolder("collect-2", "visitor-hash") {
		t.Fatal("collection submission folder was reused across shares")
	}
	if first == collectionSubmissionFolder("collect-1", "other-visitor") {
		t.Fatal("collection submission folder was reused across visitors")
	}
}

func TestVisibleCollectionUploadsHidePhysicalFolder(t *testing.T) {
	submission := &model.CollectionSubmission{FolderName: "submission-private"}
	uploads := []model.CollectionUpload{
		{FileName: "submission-private/report.pdf"},
		{FileName: "submission-private/folder/photo.jpg"},
		{FileName: "submission-other/secret.txt"},
	}
	visible := visibleCollectionUploads(uploads, submission)
	if len(visible) != 2 || visible[0].FileName != "report.pdf" || visible[1].FileName != "folder/photo.jpg" {
		t.Fatalf("unexpected visible uploads: %#v", visible)
	}
	if len(visibleCollectionUploads(uploads, nil)) != 0 {
		t.Fatal("uploads were visible without a visitor submission")
	}
}

func TestRenderCollectionReadmeEscapesValues(t *testing.T) {
	fields := []model.CollectionField{{Name: "姓名"}, {Name: "备注"}}
	readme := renderCollectionReadme(fields, map[string]string{
		"姓名": "Alice | Bob",
		"备注": "<script>alert(1)</script>\nnext",
	})
	for _, want := range []string{"Alice \\| Bob", "&lt;script&gt;alert(1)&lt;/script&gt;<br>next"} {
		if !strings.Contains(readme, want) {
			t.Fatalf("README does not contain %q: %s", want, readme)
		}
	}
}
