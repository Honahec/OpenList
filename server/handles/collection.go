package handles

import (
	"errors"
	"fmt"
	"net/url"
	stdpath "path"
	"strings"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/OpenListTeam/OpenList/v4/internal/fs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"github.com/OpenListTeam/OpenList/v4/internal/sign"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils/random"
	"github.com/OpenListTeam/OpenList/v4/server/common"
	"github.com/gin-gonic/gin"
)

type collectionReq struct {
	Password      string `json:"password" form:"password"`
	FileName      string `json:"file_name" form:"file_name"`
	FileSize      int64  `json:"file_size" form:"file_size"`
	UploadID      string `json:"upload_id" form:"upload_id"`
	PartNumber    int64  `json:"part_number" form:"part_number"`
	UploadToken   string `json:"upload_token" form:"upload_token"`
	UploadSession string `json:"upload_session" form:"upload_session"`
}

type collectionInfo struct {
	ID        string     `json:"id"`
	Remark    string     `json:"remark"`
	Readme    string     `json:"readme"`
	Header    string     `json:"header"`
	Expires   *time.Time `json:"expires"`
	Remaining int        `json:"remaining"`
	Tools     []string   `json:"direct_upload_tools"`
}

type collectionUploadInfo struct {
	FileName      string `json:"file_name"`
	UploadToken   string `json:"upload_token"`
	UploadSession string `json:"upload_session"`
	UploadInfo    any    `json:"upload_info"`
}

func getCollection(c *gin.Context, password string) (*model.Sharing, string, error) {
	s, err := op.GetSharingById(c.Param("id"))
	if err != nil || !s.Collect || !s.Valid() {
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

func CollectionInfo(c *gin.Context) {
	s, target, err := getCollection(c, c.Query("password"))
	if err != nil {
		common.ErrorResp(c, err, 403)
		return
	}
	storage, err := fs.GetStorage(target, &fs.GetStoragesArgs{})
	if err != nil {
		common.ErrorResp(c, err, 500)
		return
	}
	remaining := -1
	if s.MaxAccessed > 0 {
		remaining = s.MaxAccessed - s.Accessed
	}
	common.SuccessResp(c, collectionInfo{
		ID: s.ID, Remark: s.Remark, Readme: s.Readme, Header: s.Header,
		Expires: s.Expires, Remaining: remaining, Tools: op.GetDirectUploadTools(storage),
	})
}

func CollectionGetDirectUploadInfo(c *gin.Context) {
	var req collectionReq
	if err := c.ShouldBind(&req); err != nil {
		common.ErrorResp(c, err, 400)
		return
	}
	s, target, err := getCollection(c, req.Password)
	if err != nil {
		common.ErrorResp(c, err, 403)
		return
	}
	_ = s
	name, err := collectionFileName(req.FileName)
	if err != nil {
		common.ErrorResp(c, err, 400)
		return
	}
	name = uniqueCollectionName(c, target, name)
	info, err := fs.GetDirectUploadInfo(c, "HttpDirect", target, name, req.FileSize, false)
	if err != nil {
		common.ErrorResp(c, err, 500)
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
		UploadID: uploadID, Expires: expires,
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

func CollectionGetDirectUploadPartInfo(c *gin.Context) {
	var req collectionReq
	if err := c.ShouldBind(&req); err != nil {
		common.ErrorResp(c, err, 400)
		return
	}
	_, target, err := getCollection(c, req.Password)
	if err != nil {
		common.ErrorResp(c, err, 403)
		return
	}
	if req.UploadID == "" || req.PartNumber < 1 {
		common.ErrorStrResp(c, "invalid multipart upload", 400)
		return
	}
	if _, err := verifyCollectionUpload(c.Param("id"), req); err != nil {
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
	s, target, err := getCollection(c, req.Password)
	if err != nil {
		common.ErrorResp(c, err, 403)
		return
	}
	upload, err := verifyCollectionUpload(s.ID, req)
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
	_, target, err := getCollection(c, req.Password)
	if err != nil {
		common.ErrorResp(c, err, 403)
		return
	}
	upload, err := verifyCollectionUpload(c.Param("id"), req)
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

func collectionFileName(raw string) (string, error) {
	name, err := url.PathUnescape(strings.TrimSpace(raw))
	if err != nil {
		return "", err
	}
	name = stdpath.Base(name)
	if err := checkRelativePath(name); err != nil || name == "." || name == "/" {
		return "", errors.New("invalid file name")
	}
	return name, nil
}

func uniqueCollectionName(c *gin.Context, target, name string) string {
	if obj, _ := fs.Get(c.Request.Context(), stdpath.Join(target, name), &fs.GetArgs{NoLog: true}); obj == nil {
		return name
	}
	ext := stdpath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	return base + "-" + random.String(8) + ext
}

func collectionUploadTokenData(id, sessionID, name string, size int64, uploadID string) string {
	return fmt.Sprintf("collection:%s:%s:%s:%d:%s", id, sessionID, name, size, uploadID)
}

func verifyCollectionUpload(id string, req collectionReq) (*model.CollectionUpload, error) {
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
	if time.Now().After(upload.Expires) {
		return nil, errors.New("collection upload session expired")
	}
	return upload, nil
}
