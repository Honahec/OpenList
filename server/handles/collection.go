package handles

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	stdpath "path"
	"sort"
	"strings"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/OpenListTeam/OpenList/v4/internal/fs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"github.com/OpenListTeam/OpenList/v4/internal/sign"
	"github.com/OpenListTeam/OpenList/v4/internal/stream"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils/random"
	"github.com/OpenListTeam/OpenList/v4/server/common"
	"github.com/gin-gonic/gin"
)

type collectionReq struct {
	Password      string   `json:"password" form:"password"`
	FileName      string   `json:"file_name" form:"file_name"`
	FileSize      int64    `json:"file_size" form:"file_size"`
	UploadID      string   `json:"upload_id" form:"upload_id"`
	PartNumber    int64    `json:"part_number" form:"part_number"`
	UploadToken   string   `json:"upload_token" form:"upload_token"`
	UploadSession string   `json:"upload_session" form:"upload_session"`
	PartHashes    []string `json:"part_hashes" form:"part_hashes"`
}

type collectionUploadInfo struct {
	FileName      string `json:"file_name"`
	UploadToken   string `json:"upload_token"`
	UploadSession string `json:"upload_session"`
	UploadInfo    any    `json:"upload_info"`
}

type collectionSubmissionReq struct {
	Password string            `json:"password" form:"password"`
	Values   map[string]string `json:"values" form:"values"`
}

type collectionFormResp struct {
	Fields    []model.CollectionField `json:"fields"`
	Values    map[string]string       `json:"values"`
	Submitted bool                    `json:"submitted"`
}

const (
	collectionVisitorCookie = "openlist_collection_visitor"
	collectionVisitorLength = 48
	collectionVisitorMaxAge = 365 * 24 * 60 * 60
	collectionMaxFields     = 20
	collectionMaxFieldName  = 64
	collectionMaxValue      = 2048
)

func getCollection(c *gin.Context, id, password string) (*model.Sharing, string, error) {
	s, err := op.GetSharingById(id)
	if err != nil || !s.Collect || !s.ValidForCollectionView() {
		return nil, "", errs.InvalidSharing
	}
	if !s.Verify(password) {
		return nil, "", errs.WrongShareCode
	}
	if len(s.Files) != 1 {
		return nil, "", errors.New("collection must contain exactly one target folder")
	}
	target, err := op.GetSharingUnwrapPath(s, "/")
	if err != nil {
		return nil, "", err
	}
	obj, err := fs.Get(c.Request.Context(), target, &fs.GetArgs{NoLog: true})
	if err != nil || obj == nil || !obj.IsDir() {
		return nil, "", errors.New("collection target is not a folder")
	}
	return s, target, nil
}

func CollectionGet(c *gin.Context, req *FsGetReq) {
	id, relative, err := collectionPathFromPath(req.Path)
	if err != nil {
		common.ErrorResp(c, err, 400)
		return
	}
	s, _, err := getCollection(c, id, req.Password)
	if err != nil {
		common.ErrorResp(c, err, 403)
		return
	}
	visitorHash := collectionVisitorHash(c)
	fields, submission, _, err := collectionSubmissionState(s, visitorHash)
	if err != nil {
		common.ErrorResp(c, err, 500)
		return
	}
	readme := collectionReadme(s)
	if len(fields) > 0 && submission != nil {
		readme = renderCollectionReadme(fields, submission.Values)
	}
	if relative == "" {
		common.SuccessResp(c, FsGetResp{
			ObjResp: ObjResp{Name: id, IsDir: true, Modified: time.Time{}, Created: time.Time{}},
			Readme:  readme, Header: s.Header, Provider: "collection", Related: []ObjResp{},
		})
		return
	}
	uploads, err := op.GetCompletedCollectionUploads(id, visitorHash)
	if err != nil {
		common.ErrorResp(c, err, 500)
		return
	}
	if len(fields) > 0 {
		uploads = visibleCollectionUploads(uploads, submission)
	}
	obj, ok := collectionObject(uploads, relative)
	if !ok {
		common.ErrorStrResp(c, "object not found", 404)
		return
	}
	common.SuccessResp(c, FsGetResp{
		ObjResp: obj,
		Readme:  readme, Header: s.Header, Provider: "collection", Related: []ObjResp{},
	})
}

func CollectionList(c *gin.Context, req *ListReq) {
	id, relative, err := collectionPathFromPath(req.Path)
	if err != nil {
		common.ErrorResp(c, err, 400)
		return
	}
	s, target, err := getCollection(c, id, req.Password)
	if err != nil {
		common.ErrorResp(c, err, 403)
		return
	}
	storage, err := fs.GetStorage(target, &fs.GetStoragesArgs{})
	if err != nil {
		common.ErrorResp(c, err, 500)
		return
	}
	visitorHash := collectionVisitorHash(c)
	fields, submission, collectionForm, err := collectionSubmissionState(s, visitorHash)
	if err != nil {
		common.ErrorResp(c, err, 500)
		return
	}
	uploads, err := op.GetCompletedCollectionUploads(id, visitorHash)
	if err != nil {
		common.ErrorResp(c, err, 500)
		return
	}
	if len(fields) > 0 {
		uploads = visibleCollectionUploads(uploads, submission)
	}
	content, exists := collectionChildren(uploads, relative)
	if !exists {
		common.ErrorStrResp(c, "object not found", 404)
		return
	}
	total := len(content)
	content = paginateCollectionObjects(content, req.Page, req.PerPage)
	canUpload := s.Valid() && (collectionForm == nil || collectionForm.Submitted)
	var directUploadTools []string
	if canUpload {
		directUploadTools = op.GetDirectUploadTools(storage)
	}
	readme := collectionReadme(s)
	if len(fields) > 0 && submission != nil {
		readme = renderCollectionReadme(fields, submission.Values)
	}
	common.SuccessResp(c, FsListResp{
		Content: content, Total: int64(total), Readme: readme, Header: s.Header,
		Write: canUpload, WriteContentBypass: canUpload, Provider: "collection",
		DirectUploadTools: directUploadTools, CollectionForm: collectionForm,
	})
}

func CollectionGetDirectUploadInfo(c *gin.Context) {
	var req collectionReq
	if err := c.ShouldBind(&req); err != nil {
		common.ErrorResp(c, err, 400)
		return
	}
	s, target, err := getCollection(c, c.Param("id"), req.Password)
	if err != nil {
		common.ErrorResp(c, err, 403)
		return
	}
	if !s.Valid() {
		common.ErrorResp(c, errs.InvalidSharing, 403)
		return
	}
	name, err := collectionFileName(req.FileName)
	if err != nil {
		common.ErrorResp(c, err, 400)
		return
	}
	visitorHash := collectionVisitorHash(c)
	fields, submission, form, err := collectionSubmissionState(s, visitorHash)
	if err != nil {
		common.ErrorResp(c, err, 500)
		return
	}
	if len(fields) > 0 {
		if submission == nil || form == nil || !form.Submitted {
			common.ErrorStrResp(c, "collection submission is required before uploading", 403)
			return
		}
		if strings.EqualFold(name, "README.md") {
			common.ErrorStrResp(c, "README.md is managed by the collection form", 400)
			return
		}
		name = stdpath.Join(submission.FolderName, name)
	}
	name = uniqueCollectionName(c, target, name)
	info, err := fs.GetDirectUploadInfo(c, "HttpDirect", target, name, req.FileSize, false, req.PartHashes)
	if err != nil {
		common.ErrorResp(c, err, 500)
		return
	}
	if directUploadNeedsHashing(info) {
		common.SuccessResp(c, collectionUploadInfo{FileName: name, UploadInfo: info})
		return
	}
	uploadID := ""
	switch httpInfo := info.(type) {
	case *model.HttpDirectUploadInfo:
		if httpInfo.Multipart != nil {
			uploadID = httpInfo.Multipart.UploadID
		}
	case model.HttpDirectUploadInfo:
		if httpInfo.Multipart != nil {
			uploadID = httpInfo.Multipart.UploadID
		}
	}
	sessionID := random.String(24)
	expires := time.Now().Add(4 * time.Hour)
	if err := op.CreateCollectionUpload(&model.CollectionUpload{
		ID: sessionID, SharingID: s.ID, FileName: name, FileSize: req.FileSize,
		UploadID: uploadID, Expires: expires, VisitorHash: visitorHash,
	}); err != nil {
		if uploadID != "" {
			_ = fs.AbortDirectUpload(c, target, name, uploadID)
		}
		common.ErrorResp(c, err, 500)
		return
	}
	token := sign.WithDuration(collectionUploadTokenData(s.ID, sessionID, name, req.FileSize, uploadID), 4*time.Hour)
	common.SuccessResp(c, collectionUploadInfo{FileName: name, UploadToken: token, UploadSession: sessionID, UploadInfo: info})
}

func directUploadNeedsHashing(info any) bool {
	switch httpInfo := info.(type) {
	case *model.HttpDirectUploadInfo:
		return httpInfo != nil && httpInfo.Hashing != nil
	case model.HttpDirectUploadInfo:
		return httpInfo.Hashing != nil
	default:
		return false
	}
}

func CollectionGetDirectUploadPartInfo(c *gin.Context) {
	var req collectionReq
	if err := c.ShouldBind(&req); err != nil {
		common.ErrorResp(c, err, 400)
		return
	}
	_, target, err := getCollection(c, c.Param("id"), req.Password)
	if err != nil {
		common.ErrorResp(c, err, 403)
		return
	}
	if req.UploadID == "" || req.PartNumber < 1 {
		common.ErrorStrResp(c, "invalid multipart upload", 400)
		return
	}
	if _, err := verifyCollectionUpload(c, c.Param("id"), req); err != nil {
		common.ErrorResp(c, err, 403)
		return
	}
	info, err := fs.GetDirectUploadPartInfo(c, target, req.FileName, req.UploadID, req.PartNumber)
	if err != nil {
		common.ErrorResp(c, err, 500)
		return
	}
	common.SuccessResp(c, info)
}

func CollectionCompleteDirectUpload(c *gin.Context) {
	var req collectionReq
	if err := c.ShouldBind(&req); err != nil {
		common.ErrorResp(c, err, 400)
		return
	}
	s, target, err := getCollection(c, c.Param("id"), req.Password)
	if err != nil {
		common.ErrorResp(c, err, 403)
		return
	}
	upload, err := verifyCollectionUpload(c, s.ID, req)
	if err != nil {
		common.ErrorResp(c, err, 403)
		return
	}
	if upload.Completed {
		common.SuccessResp(c)
		return
	}
	if err := fs.CompleteDirectUpload(c, target, req.FileName, req.UploadID, req.FileSize); err != nil {
		common.ErrorResp(c, err, 500)
		return
	}
	if _, err := op.CompleteCollectionUpload(upload.ID, s.ID); err != nil {
		_ = fs.Remove(c, stdpath.Join(target, req.FileName))
		common.ErrorResp(c, err, 500)
		return
	}
	common.SuccessResp(c)
}

func CollectionAbortDirectUpload(c *gin.Context) {
	var req collectionReq
	if err := c.ShouldBind(&req); err != nil {
		common.ErrorResp(c, err, 400)
		return
	}
	_, target, err := getCollection(c, c.Param("id"), req.Password)
	if err != nil {
		common.ErrorResp(c, err, 403)
		return
	}
	upload, err := verifyCollectionUpload(c, c.Param("id"), req)
	if err != nil {
		common.ErrorResp(c, err, 403)
		return
	}
	if upload.Completed {
		common.ErrorStrResp(c, "collection upload already completed", 409)
		return
	}
	if req.UploadID != "" {
		err = fs.AbortDirectUpload(c, target, req.FileName, req.UploadID)
	}
	if err != nil {
		common.ErrorResp(c, err, 500)
		return
	}
	_ = op.DeleteCollectionUpload(upload.ID)
	common.SuccessResp(c)
}

func CollectionUpdateSubmission(c *gin.Context) {
	var req collectionSubmissionReq
	if err := c.ShouldBind(&req); err != nil {
		common.ErrorResp(c, err, 400)
		return
	}
	s, target, err := getCollection(c, c.Param("id"), req.Password)
	if err != nil {
		common.ErrorResp(c, err, 403)
		return
	}
	fields, err := parseCollectionFields(s.CollectionFields)
	if err != nil {
		common.ErrorResp(c, err, 500)
		return
	}
	if len(fields) == 0 {
		common.ErrorStrResp(c, "collection submission fields are not enabled", 400)
		return
	}
	values, err := validateCollectionValues(fields, req.Values)
	if err != nil {
		common.ErrorResp(c, err, 400)
		return
	}
	visitorHash := collectionVisitorHash(c)
	submission, err := op.GetCollectionSubmission(s.ID, visitorHash)
	if err != nil {
		common.ErrorResp(c, err, 500)
		return
	}
	if submission == nil {
		submission = &model.CollectionSubmission{
			SharingID: s.ID, VisitorHash: visitorHash,
			FolderName: collectionSubmissionFolder(s.ID, visitorHash),
		}
	}
	submission.Values = values
	readme := renderCollectionReadme(fields, values)
	if err := writeCollectionReadme(c, target, submission.FolderName, readme); err != nil {
		common.ErrorResp(c, err, 500)
		return
	}
	if err := op.SaveCollectionSubmission(submission); err != nil {
		common.ErrorResp(c, err, 500)
		return
	}
	common.SuccessResp(c, collectionFormResp{Fields: fields, Values: values, Submitted: true})
}

func parseCollectionFields(raw string) ([]model.CollectionField, error) {
	if len(raw) > 4096 {
		return nil, errors.New("collection fields are too long")
	}
	fields := make([]model.CollectionField, 0)
	seen := make(map[string]struct{})
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		required := strings.HasPrefix(line, "*")
		if required {
			line = strings.TrimSpace(strings.TrimPrefix(line, "*"))
		}
		if line == "" {
			return nil, errors.New("collection field name cannot be empty")
		}
		if len([]rune(line)) > collectionMaxFieldName {
			return nil, errors.New("collection field name is too long")
		}
		if _, ok := seen[line]; ok {
			return nil, fmt.Errorf("duplicate collection field: %s", line)
		}
		seen[line] = struct{}{}
		fields = append(fields, model.CollectionField{Name: line, Required: required})
		if len(fields) > collectionMaxFields {
			return nil, fmt.Errorf("collection fields cannot exceed %d", collectionMaxFields)
		}
	}
	return fields, nil
}

func validateCollectionValues(fields []model.CollectionField, input map[string]string) (map[string]string, error) {
	values := make(map[string]string, len(fields))
	for _, field := range fields {
		value := strings.TrimSpace(input[field.Name])
		if field.Required && value == "" {
			return nil, fmt.Errorf("collection field [%s] is required", field.Name)
		}
		if len([]rune(value)) > collectionMaxValue {
			return nil, fmt.Errorf("collection field [%s] is too long", field.Name)
		}
		values[field.Name] = value
	}
	return values, nil
}

func collectionSubmissionState(s *model.Sharing, visitorHash string) ([]model.CollectionField, *model.CollectionSubmission, *collectionFormResp, error) {
	fields, err := parseCollectionFields(s.CollectionFields)
	if err != nil || len(fields) == 0 {
		return fields, nil, nil, err
	}
	submission, err := op.GetCollectionSubmission(s.ID, visitorHash)
	if err != nil {
		return nil, nil, nil, err
	}
	values := make(map[string]string)
	submitted := false
	if submission != nil {
		for _, field := range fields {
			values[field.Name] = submission.Values[field.Name]
		}
		_, validationErr := validateCollectionValues(fields, values)
		submitted = validationErr == nil
	}
	return fields, submission, &collectionFormResp{Fields: fields, Values: values, Submitted: submitted}, nil
}

func collectionSubmissionFolder(sharingID, visitorHash string) string {
	digest := sha256.Sum256([]byte(sharingID + "\x00" + visitorHash))
	return "submission-" + hex.EncodeToString(digest[:12])
}

func visibleCollectionUploads(uploads []model.CollectionUpload, submission *model.CollectionSubmission) []model.CollectionUpload {
	if submission == nil {
		return []model.CollectionUpload{}
	}
	prefix := submission.FolderName + "/"
	visible := make([]model.CollectionUpload, 0, len(uploads))
	for _, upload := range uploads {
		if !strings.HasPrefix(upload.FileName, prefix) {
			continue
		}
		upload.FileName = strings.TrimPrefix(upload.FileName, prefix)
		if upload.FileName != "" {
			visible = append(visible, upload)
		}
	}
	return visible
}

func renderCollectionReadme(fields []model.CollectionField, values map[string]string) string {
	var builder strings.Builder
	builder.WriteString("# 提交信息\n\n| 字段 | 填写内容 |\n| --- | --- |\n")
	for _, field := range fields {
		builder.WriteString("| ")
		builder.WriteString(collectionMarkdownCell(field.Name))
		builder.WriteString(" | ")
		builder.WriteString(collectionMarkdownCell(values[field.Name]))
		builder.WriteString(" |\n")
	}
	return builder.String()
}

func collectionMarkdownCell(value string) string {
	value = html.EscapeString(value)
	value = strings.ReplaceAll(value, "|", "\\|")
	value = strings.ReplaceAll(value, "\r\n", "<br>")
	value = strings.ReplaceAll(value, "\n", "<br>")
	return value
}

func writeCollectionReadme(c *gin.Context, target, folder, content string) error {
	now := time.Now()
	file := &stream.FileStream{
		Ctx: c.Request.Context(),
		Obj: &model.Object{
			Name: "README.md", Size: int64(len(content)), Modified: now, Ctime: now,
		},
		Reader:   bytes.NewReader([]byte(content)),
		Mimetype: "text/markdown; charset=utf-8",
	}
	return fs.PutDirectly(c.Request.Context(), stdpath.Join(target, folder), file)
}

func collectionFileName(raw string) (string, error) {
	name, err := url.PathUnescape(strings.TrimSpace(raw))
	if err != nil {
		return "", err
	}
	name = strings.TrimPrefix(name, "/")
	if name == "" || strings.Contains(name, "\\") {
		return "", errors.New("invalid file name")
	}
	parts := strings.Split(name, "/")
	for _, part := range parts {
		if err := checkRelativePath(part); err != nil {
			return "", errors.New("invalid file name")
		}
	}
	clean := stdpath.Clean(name)
	if clean != name {
		return "", errors.New("invalid file name")
	}
	return clean, nil
}

func uniqueCollectionName(c *gin.Context, target, name string) string {
	if obj, _ := fs.Get(c.Request.Context(), stdpath.Join(target, name), &fs.GetArgs{NoLog: true}); obj == nil {
		return name
	}
	dir, file := stdpath.Split(name)
	ext := stdpath.Ext(file)
	base := strings.TrimSuffix(file, ext)
	return stdpath.Join(dir, base+"-"+random.String(8)+ext)
}

func collectionPathFromPath(raw string) (string, string, error) {
	value := strings.TrimPrefix(raw, "/@c")
	value = strings.TrimPrefix(value, "/")
	if value == "" {
		return "", "", errors.New("invalid collection path")
	}
	idRaw, relativeRaw, _ := strings.Cut(value, "/")
	id, err := url.PathUnescape(idRaw)
	if err != nil || id == "" || strings.Contains(id, "/") {
		return "", "", errors.New("invalid collection path")
	}
	if relativeRaw == "" {
		return id, "", nil
	}
	relative, err := collectionFileName(relativeRaw)
	if err != nil {
		return "", "", errors.New("invalid collection path")
	}
	return id, relative, nil
}

func collectionVisitorHash(c *gin.Context) string {
	visitor, err := c.Cookie(collectionVisitorCookie)
	if err != nil || !validCollectionVisitor(visitor) {
		visitor = random.String(collectionVisitorLength)
		c.SetSameSite(http.SameSiteLaxMode)
		c.SetCookie(collectionVisitorCookie, visitor, collectionVisitorMaxAge, "/", "", collectionCookieSecure(c), true)
	}
	digest := sha256.Sum256([]byte(visitor))
	return hex.EncodeToString(digest[:])
}

func validCollectionVisitor(visitor string) bool {
	if len(visitor) != collectionVisitorLength {
		return false
	}
	for _, char := range visitor {
		if !((char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9')) {
			return false
		}
	}
	return true
}

func collectionCookieSecure(c *gin.Context) bool {
	if c.Request.TLS != nil {
		return true
	}
	proto := strings.TrimSpace(strings.Split(c.GetHeader("X-Forwarded-Proto"), ",")[0])
	return strings.EqualFold(proto, "https")
}

func collectionObject(uploads []model.CollectionUpload, relative string) (ObjResp, bool) {
	var dirTime time.Time
	for _, upload := range uploads {
		completedAt := collectionCompletedAt(upload)
		if upload.FileName == relative {
			return ObjResp{
				Name: stdpath.Base(relative), Size: upload.FileSize, IsDir: false,
				Modified: completedAt, Created: completedAt,
			}, true
		}
		if strings.HasPrefix(upload.FileName, relative+"/") && completedAt.After(dirTime) {
			dirTime = completedAt
		}
	}
	if !dirTime.IsZero() {
		return ObjResp{
			Name: stdpath.Base(relative), IsDir: true, Type: 1,
			Modified: dirTime, Created: dirTime,
		}, true
	}
	return ObjResp{}, false
}

func collectionChildren(uploads []model.CollectionUpload, relative string) ([]ObjResp, bool) {
	children := make(map[string]ObjResp)
	exists := relative == ""
	prefix := ""
	if relative != "" {
		prefix = relative + "/"
	}
	for _, upload := range uploads {
		if relative != "" && !strings.HasPrefix(upload.FileName, prefix) {
			continue
		}
		rest := strings.TrimPrefix(upload.FileName, prefix)
		if rest == "" {
			continue
		}
		exists = true
		name, _, nested := strings.Cut(rest, "/")
		completedAt := collectionCompletedAt(upload)
		current, found := children[name]
		if nested {
			if !found || !current.IsDir || completedAt.After(current.Modified) {
				children[name] = ObjResp{
					Name: name, IsDir: true, Type: 1,
					Modified: completedAt, Created: completedAt,
				}
			}
			continue
		}
		if found && current.IsDir {
			continue
		}
		children[name] = ObjResp{
			Name: name, Size: upload.FileSize, IsDir: false,
			Modified: completedAt, Created: completedAt,
		}
	}
	content := make([]ObjResp, 0, len(children))
	for _, child := range children {
		content = append(content, child)
	}
	sort.Slice(content, func(i, j int) bool {
		if content[i].IsDir != content[j].IsDir {
			return content[i].IsDir
		}
		return strings.ToLower(content[i].Name) < strings.ToLower(content[j].Name)
	})
	return content, exists
}

func collectionCompletedAt(upload model.CollectionUpload) time.Time {
	if upload.CompletedAt != nil {
		return *upload.CompletedAt
	}
	return time.Time{}
}

func paginateCollectionObjects(content []ObjResp, page, perPage int) []ObjResp {
	if len(content) == 0 || page < 1 || perPage < 1 {
		return content
	}
	if page-1 > len(content)/perPage {
		return []ObjResp{}
	}
	start := (page - 1) * perPage
	if start >= len(content) {
		return []ObjResp{}
	}
	end := start + perPage
	if end < start || end > len(content) {
		end = len(content)
	}
	return content[start:end]
}

func collectionIDFromPath(raw string) (string, error) {
	id, relative, err := collectionPathFromPath(raw)
	if err != nil || relative != "" {
		return "", errors.New("invalid collection path")
	}
	return id, nil
}

func collectionReadme(s *model.Sharing) string {
	if strings.TrimSpace(s.Readme) != "" {
		return s.Readme
	}
	return s.Remark
}

func collectionUploadTokenData(id, sessionID, name string, size int64, uploadID string) string {
	return fmt.Sprintf("collection:%s:%s:%s:%d:%s", id, sessionID, name, size, uploadID)
}

func verifyCollectionUpload(c *gin.Context, id string, req collectionReq) (*model.CollectionUpload, error) {
	name, err := collectionFileName(req.FileName)
	if err != nil {
		return nil, err
	}
	if req.UploadSession == "" {
		return nil, errors.New("collection upload session is required")
	}
	if err := sign.Verify(collectionUploadTokenData(id, req.UploadSession, name, req.FileSize, req.UploadID), req.UploadToken); err != nil {
		return nil, err
	}
	upload, err := op.GetCollectionUpload(req.UploadSession)
	if err != nil {
		return nil, err
	}
	if upload.SharingID != id || upload.FileName != name || upload.FileSize != req.FileSize || upload.UploadID != req.UploadID {
		return nil, errors.New("collection upload session does not match request")
	}
	if upload.VisitorHash == "" || upload.VisitorHash != collectionVisitorHash(c) {
		return nil, errors.New("collection upload session belongs to another visitor")
	}
	if time.Now().After(upload.Expires) {
		return nil, errors.New("collection upload session expired")
	}
	return upload, nil
}
