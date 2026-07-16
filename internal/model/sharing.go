package model

import "time"

type SharingDB struct {
	ID               string     `json:"id" gorm:"type:varchar(64);primaryKey"`
	FilesRaw         string     `json:"-" gorm:"type:text"`
	Expires          *time.Time `json:"expires"`
	Pwd              string     `json:"pwd"`
	Accessed         int        `json:"accessed"`
	MaxAccessed      int        `json:"max_accessed"`
	CreatorId        uint       `json:"-"`
	Disabled         bool       `json:"disabled"`
	Remark           string     `json:"remark"`
	Readme           string     `json:"readme" gorm:"type:text"`
	Header           string     `json:"header" gorm:"type:text"`
	Collect          bool       `json:"collect"`
	CollectionFields string     `json:"collection_fields" gorm:"type:text"`
	Sort
}

type Sharing struct {
	*SharingDB
	Files   []string `json:"files"`
	Creator *User    `json:"-"`
}

type CollectionUpload struct {
	ID          string     `json:"id" gorm:"type:varchar(32);primaryKey"`
	SharingID   string     `json:"sharing_id" gorm:"type:varchar(64);index;index:idx_collection_visitor,priority:1"`
	VisitorHash string     `json:"-" gorm:"type:char(64);index:idx_collection_visitor,priority:2"`
	FileName    string     `json:"file_name" gorm:"type:text"`
	FileSize    int64      `json:"file_size"`
	UploadID    string     `json:"upload_id" gorm:"type:text"`
	Expires     time.Time  `json:"expires" gorm:"index"`
	Completed   bool       `json:"completed" gorm:"index:idx_collection_visitor,priority:3"`
	CompletedAt *time.Time `json:"completed_at"`
}

type CollectionField struct {
	Name     string `json:"name"`
	Required bool   `json:"required"`
}

type CollectionSubmission struct {
	ID          uint              `json:"-" gorm:"primaryKey"`
	SharingID   string            `json:"sharing_id" gorm:"type:varchar(64);uniqueIndex:idx_collection_submission"`
	VisitorHash string            `json:"-" gorm:"type:char(64);uniqueIndex:idx_collection_submission"`
	FolderName  string            `json:"folder_name" gorm:"type:varchar(64)"`
	ValuesRaw   string            `json:"-" gorm:"type:text"`
	Values      map[string]string `json:"values" gorm:"-"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

func (s *Sharing) Valid() bool {
	if !s.ValidForCollectionView() {
		return false
	}
	if s.MaxAccessed > 0 && s.Accessed >= s.MaxAccessed {
		return false
	}
	return true
}

func (s *Sharing) ValidForCollectionView() bool {
	if s.Disabled {
		return false
	}
	if len(s.Files) == 0 {
		return false
	}
	if s.Creator == nil || !s.Creator.CanShare() {
		return false
	}
	if s.Expires != nil && !s.Expires.IsZero() && s.Expires.Before(time.Now()) {
		return false
	}
	return true
}

func (s *Sharing) ValidForRead() bool {
	return !s.Collect && s.Valid()
}

func (s *Sharing) Verify(pwd string) bool {
	return s.Pwd == "" || s.Pwd == pwd
}
