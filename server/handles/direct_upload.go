package handles

import (
	"net/url"
	stdpath "path"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/OpenListTeam/OpenList/v4/internal/fs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/server/common"
	"github.com/gin-gonic/gin"
)

type FsGetDirectUploadInfoReq struct {
	Path       string   `json:"path" form:"path"`
	FileName   string   `json:"file_name" form:"file_name"`
	FileSize   int64    `json:"file_size" form:"file_size"`
	Tool       string   `json:"tool" form:"tool"`
	PartHashes []string `json:"part_hashes" form:"part_hashes"`
}

type FsDirectUploadSessionReq struct {
	Path       string `json:"path" form:"path"`
	FileName   string `json:"file_name" form:"file_name"`
	FileSize   int64  `json:"file_size" form:"file_size"`
	UploadID   string `json:"upload_id" form:"upload_id"`
	PartNumber int64  `json:"part_number" form:"part_number"`
}

// FsGetDirectUploadInfo returns the direct upload info if supported by the driver
// If the driver does not support direct upload, returns null for upload_info
func FsGetDirectUploadInfo(c *gin.Context) {
	var req FsGetDirectUploadInfoReq
	if err := c.ShouldBind(&req); err != nil {
		common.ErrorResp(c, err, 400)
		return
	}
	// Decode path
	path, err := url.PathUnescape(req.Path)
	if err != nil {
		common.ErrorResp(c, err, 400)
		return
	}
	// Get user and join path
	user := c.Request.Context().Value(conf.UserKey).(*model.User)
	path, err = user.JoinPath(path)
	if err != nil {
		common.ErrorResp(c, err, 403)
		return
	}
	if err := checkRelativePath(req.FileName); err != nil {
		common.ErrorResp(c, err, 403)
		return
	}
	overwrite := c.GetHeader("Overwrite") != "false"
	dstPath := stdpath.Join(path, req.FileName)
	if !overwrite {
		res, err := fs.Get(c.Request.Context(), dstPath, &fs.GetArgs{NoLog: true})
		if err != nil && !errs.IsObjectNotFound(err) {
			common.ErrorResp(c, err, 500)
			return
		}
		if res != nil {
			common.ErrorStrResp(c, "file exists", 403)
			return
		}
	}
	directUploadInfo, err := fs.GetDirectUploadInfo(c, req.Tool, path, req.FileName, req.FileSize, overwrite, req.PartHashes)
	if err != nil {
		if !overwrite && errs.IsObjectAlreadyExists(err) {
			common.ErrorStrResp(c, "file exists", 403)
			return
		}
		common.ErrorResp(c, err, 500)
		return
	}
	common.SuccessResp(c, directUploadInfo)
}

func FsGetDirectUploadPartInfo(c *gin.Context) {
	var req FsDirectUploadSessionReq
	if err := c.ShouldBind(&req); err != nil {
		common.ErrorResp(c, err, 400)
		return
	}
	path, err := resolveDirectUploadPath(c, req.Path, req.FileName)
	if err != nil {
		common.ErrorResp(c, err, 403)
		return
	}
	if req.UploadID == "" || req.PartNumber < 1 {
		common.ErrorStrResp(c, "invalid multipart upload ID or part number", 400)
		return
	}
	info, err := fs.GetDirectUploadPartInfo(c, path, req.FileName, req.UploadID, req.PartNumber)
	if err != nil {
		common.ErrorResp(c, err, 500)
		return
	}
	common.SuccessResp(c, info)
}

func FsCompleteDirectUpload(c *gin.Context) {
	var req FsDirectUploadSessionReq
	if err := c.ShouldBind(&req); err != nil {
		common.ErrorResp(c, err, 400)
		return
	}
	path, err := resolveDirectUploadPath(c, req.Path, req.FileName)
	if err != nil {
		common.ErrorResp(c, err, 403)
		return
	}
	if err := fs.CompleteDirectUpload(c, path, req.FileName, req.UploadID, req.FileSize); err != nil {
		common.ErrorResp(c, err, 500)
		return
	}
	common.SuccessResp(c)
}

func FsAbortDirectUpload(c *gin.Context) {
	var req FsDirectUploadSessionReq
	if err := c.ShouldBind(&req); err != nil {
		common.ErrorResp(c, err, 400)
		return
	}
	path, err := resolveDirectUploadPath(c, req.Path, req.FileName)
	if err != nil {
		common.ErrorResp(c, err, 403)
		return
	}
	if req.UploadID == "" {
		common.ErrorStrResp(c, "multipart upload ID is required", 400)
		return
	}
	if err := fs.AbortDirectUpload(c, path, req.FileName, req.UploadID); err != nil {
		common.ErrorResp(c, err, 500)
		return
	}
	common.SuccessResp(c)
}

func resolveDirectUploadPath(c *gin.Context, rawPath, fileName string) (string, error) {
	path, err := url.PathUnescape(rawPath)
	if err != nil {
		return "", err
	}
	if err := checkRelativePath(fileName); err != nil {
		return "", err
	}
	user := c.Request.Context().Value(conf.UserKey).(*model.User)
	return user.JoinPath(path)
}
