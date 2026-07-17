package model

type HttpDirectUploadInfo struct {
	UploadURL string                         `json:"upload_url"`          // The URL to upload the file
	ChunkSize int64                          `json:"chunk_size"`          // The chunk size for uploading, 0 means no chunking required
	Headers   map[string]string              `json:"headers,omitempty"`   // Optional headers to include in the upload request
	Method    string                         `json:"method,omitempty"`    // HTTP method, default is PUT
	Finalize  bool                           `json:"finalize,omitempty"`  // Ask the client to confirm the upload with OpenList
	Completed bool                           `json:"completed,omitempty"` // The provider completed the upload during initialization
	Hashing   *HttpDirectUploadHashingInfo   `json:"hashing,omitempty"`   // Optional client-side hashing required before initialization
	Multipart *HttpDirectMultipartUploadInfo `json:"multipart,omitempty"` // Optional multipart upload session
}

type HttpDirectUploadHashingInfo struct {
	Algorithm string `json:"algorithm"`
	ChunkSize int64  `json:"chunk_size"`
}

type HttpDirectMultipartUploadInfo struct {
	UploadID string  `json:"upload_id"`
	Parts    []int64 `json:"parts,omitempty"` // Optional one-based part numbers required by the provider
}

type HttpDirectUploadPartInfo struct {
	UploadURL string            `json:"upload_url"`
	Headers   map[string]string `json:"headers,omitempty"`
	Method    string            `json:"method,omitempty"`
	BodyMode  string            `json:"body_mode,omitempty"` // raw by default; multipart wraps the part in a file form field
}
