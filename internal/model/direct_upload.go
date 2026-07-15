package model

type HttpDirectUploadInfo struct {
	UploadURL string                         `json:"upload_url"`          // The URL to upload the file
	ChunkSize int64                          `json:"chunk_size"`          // The chunk size for uploading, 0 means no chunking required
	Headers   map[string]string              `json:"headers,omitempty"`   // Optional headers to include in the upload request
	Method    string                         `json:"method,omitempty"`    // HTTP method, default is PUT
	Finalize  bool                           `json:"finalize,omitempty"`  // Ask the client to confirm the upload with OpenList
	Multipart *HttpDirectMultipartUploadInfo `json:"multipart,omitempty"` // Optional multipart upload session
}

type HttpDirectMultipartUploadInfo struct {
	UploadID string `json:"upload_id"`
}

type HttpDirectUploadPartInfo struct {
	UploadURL string            `json:"upload_url"`
	Headers   map[string]string `json:"headers,omitempty"`
	Method    string            `json:"method,omitempty"`
}
