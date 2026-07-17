package baidu_netdisk

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	cryptorand "crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	stdpath "path"
	"strconv"
	"strings"
	"time"

	"github.com/OpenListTeam/go-cache"

	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils/random"
)

const (
	baiduDirectUploadSessionExpire = 4 * time.Hour
	baiduDirectUploadTokenExpire   = 10 * time.Minute
	baiduDirectUploadAAD           = "openlist-baidu-upload-v1"
)

type baiduDirectUploadSession struct {
	StorageID     uint
	Path          string
	FileName      string
	FileSize      int64
	ProviderID    string
	UploadURL     string
	BlockHashes   []string
	RequiredParts map[int64]struct{}
	CTime         int64
	MTime         int64
}

type baiduDirectUploadPayload struct {
	Target  string `json:"target"`
	Expires int64  `json:"expires"`
}

var baiduDirectUploadSessions = cache.NewMemCache[*baiduDirectUploadSession](
	cache.WithShards[*baiduDirectUploadSession](16),
)

func (d *BaiduNetdisk) GetDirectUploadTools() []string {
	if !d.directUploadConfigured() {
		return nil
	}
	return []string{"HttpDirect"}
}

func (d *BaiduNetdisk) GetDirectUploadInfo(ctx context.Context, tool string, dstDir model.Obj, fileName string, fileSize int64) (any, error) {
	return d.GetDirectUploadInfoWithHashes(ctx, tool, dstDir, fileName, fileSize, nil)
}

func (d *BaiduNetdisk) GetDirectUploadInfoWithHashes(ctx context.Context, tool string, dstDir model.Obj, fileName string, fileSize int64, partHashes []string) (any, error) {
	if tool != "HttpDirect" || !d.directUploadConfigured() {
		return nil, errs.NotImplement
	}
	if fileSize < 1 {
		return nil, ErrBaiduEmptyFilesNotAllowed
	}
	chunkSize := d.getSliceSize(fileSize)
	if len(partHashes) == 0 {
		return &model.HttpDirectUploadInfo{
			Hashing: &model.HttpDirectUploadHashingInfo{Algorithm: "md5", ChunkSize: chunkSize},
		}, nil
	}
	if err := validateBaiduDirectUploadHashes(fileSize, chunkSize, partHashes); err != nil {
		return nil, err
	}

	blockList, err := utils.Json.MarshalToString(partHashes)
	if err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	path := stdpath.Join(dstDir.GetPath(), fileName)
	precreate, err := d.precreate(ctx, path, fileSize, blockList, "", "", now, now)
	if err != nil {
		return nil, err
	}
	if precreate.ReturnType == 2 {
		return &model.HttpDirectUploadInfo{Completed: true, Finalize: true}, nil
	}
	if precreate.Uploadid == "" {
		return nil, errors.New("baidu returned an empty direct upload ID")
	}
	uploadURL := d.getUploadUrl(path, precreate.Uploadid)
	if uploadURL == "" {
		return nil, errors.New("baidu returned an empty direct upload URL")
	}

	required := make(map[int64]struct{}, len(precreate.BlockList))
	parts := make([]int64, 0, len(precreate.BlockList))
	for _, part := range precreate.BlockList {
		if part < 0 || part >= len(partHashes) {
			return nil, fmt.Errorf("baidu returned invalid part number %d", part)
		}
		partNumber := int64(part + 1)
		required[partNumber] = struct{}{}
		parts = append(parts, partNumber)
	}

	sessionID := random.String(32)
	baiduDirectUploadSessions.Set(sessionID, &baiduDirectUploadSession{
		StorageID: d.ID, Path: path, FileName: fileName, FileSize: fileSize,
		ProviderID: precreate.Uploadid, UploadURL: uploadURL,
		BlockHashes: append([]string(nil), partHashes...), RequiredParts: required,
		CTime: now, MTime: now,
	}, cache.WithEx[*baiduDirectUploadSession](baiduDirectUploadSessionExpire))

	return &model.HttpDirectUploadInfo{
		ChunkSize: chunkSize,
		Finalize:  true,
		Multipart: &model.HttpDirectMultipartUploadInfo{UploadID: sessionID, Parts: parts},
	}, nil
}

func (d *BaiduNetdisk) GetDirectUploadPartInfo(_ context.Context, dstDir model.Obj, fileName, uploadID string, partNumber int64) (*model.HttpDirectUploadPartInfo, error) {
	session, err := d.getBaiduDirectUploadSession(dstDir, fileName, uploadID)
	if err != nil {
		return nil, err
	}
	if _, ok := session.RequiredParts[partNumber]; !ok {
		return nil, fmt.Errorf("baidu part %d is not required", partNumber)
	}

	target, err := url.Parse(strings.TrimSuffix(session.UploadURL, "/") + "/rest/2.0/pcs/superfile2")
	if err != nil {
		return nil, err
	}
	query := target.Query()
	query.Set("method", "upload")
	query.Set("access_token", d.AccessToken)
	query.Set("type", "tmpfile")
	query.Set("path", session.Path)
	query.Set("uploadid", session.ProviderID)
	query.Set("partseq", strconv.FormatInt(partNumber-1, 10))
	target.RawQuery = query.Encode()

	token, err := encryptBaiduDirectUploadPayload(d.DirectUploadGatewayKey, baiduDirectUploadPayload{
		Target: target.String(), Expires: time.Now().Add(baiduDirectUploadTokenExpire).Unix(),
	})
	if err != nil {
		return nil, err
	}
	gateway, err := url.Parse(d.DirectUploadGateway)
	if err != nil {
		return nil, err
	}
	gateway.Path = strings.TrimSuffix(gateway.Path, "/") + "/upload"
	gateway.RawQuery = url.Values{"token": {token}}.Encode()
	return &model.HttpDirectUploadPartInfo{
		UploadURL: gateway.String(), Method: http.MethodPut, BodyMode: "multipart",
	}, nil
}

func (d *BaiduNetdisk) CompleteDirectUpload(ctx context.Context, dstDir model.Obj, fileName, uploadID string) error {
	session, err := d.getBaiduDirectUploadSession(dstDir, fileName, uploadID)
	if err != nil {
		return err
	}
	blockList, err := utils.Json.MarshalToString(session.BlockHashes)
	if err != nil {
		return err
	}
	var file File
	_, err = d.create(session.Path, session.FileSize, 0, session.ProviderID, blockList, &file, session.MTime, session.CTime)
	if err != nil {
		return err
	}
	baiduDirectUploadSessions.Del(uploadID)
	return nil
}

func (d *BaiduNetdisk) AbortDirectUpload(_ context.Context, dstDir model.Obj, fileName, uploadID string) error {
	if _, err := d.getBaiduDirectUploadSession(dstDir, fileName, uploadID); err != nil {
		return err
	}
	baiduDirectUploadSessions.Del(uploadID)
	return nil
}

func (d *BaiduNetdisk) getBaiduDirectUploadSession(dstDir model.Obj, fileName, uploadID string) (*baiduDirectUploadSession, error) {
	session, ok := baiduDirectUploadSessions.Get(uploadID)
	if !ok || session == nil {
		return nil, errors.New("baidu direct upload session is missing or expired")
	}
	if session.StorageID != d.ID || session.FileName != fileName || session.Path != stdpath.Join(dstDir.GetPath(), fileName) {
		return nil, errors.New("baidu direct upload session does not match the destination")
	}
	return session, nil
}

func (d *BaiduNetdisk) directUploadConfigured() bool {
	gateway, err := url.Parse(d.DirectUploadGateway)
	if err != nil || gateway.Scheme != "https" || gateway.Host == "" {
		return false
	}
	key, err := hex.DecodeString(d.DirectUploadGatewayKey)
	return err == nil && len(key) == 32
}

func validateBaiduDirectUploadHashes(fileSize, chunkSize int64, hashes []string) error {
	if chunkSize <= 0 {
		return errors.New("invalid baidu direct upload chunk size")
	}
	want := int((fileSize + chunkSize - 1) / chunkSize)
	if len(hashes) != want {
		return fmt.Errorf("invalid baidu block hash count: expected %d, got %d", want, len(hashes))
	}
	for _, hash := range hashes {
		decoded, err := hex.DecodeString(hash)
		if err != nil || len(decoded) != 16 || hash != strings.ToLower(hash) {
			return errors.New("invalid baidu MD5 block hash")
		}
	}
	return nil
}

func encryptBaiduDirectUploadPayload(keyHex string, payload baiduDirectUploadPayload) (string, error) {
	key, err := hex.DecodeString(keyHex)
	if err != nil || len(key) != 32 {
		return "", errors.New("invalid baidu direct upload gateway key")
	}
	plaintext, err := utils.Json.Marshal(payload)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := cryptorand.Read(nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, plaintext, []byte(baiduDirectUploadAAD))
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

var _ driver.DirectUploader = (*BaiduNetdisk)(nil)
var _ driver.DirectUploadHashRequester = (*BaiduNetdisk)(nil)
var _ driver.MultipartDirectUploader = (*BaiduNetdisk)(nil)
