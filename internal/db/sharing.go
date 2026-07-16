package db

import (
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils/random"
	"github.com/pkg/errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func GetSharingById(id string) (*model.SharingDB, error) {
	s := model.SharingDB{ID: id}
	if err := db.Where(s).First(&s).Error; err != nil {
		return nil, errors.Wrapf(err, "failed get sharing")
	}
	return &s, nil
}

func GetSharings(pageIndex, pageSize int) (sharings []model.SharingDB, count int64, err error) {
	sharingDB := db.Model(&model.SharingDB{})
	if err := sharingDB.Count(&count).Error; err != nil {
		return nil, 0, errors.Wrapf(err, "failed get sharings count")
	}
	if err := sharingDB.Order(columnName("id")).Offset((pageIndex - 1) * pageSize).Limit(pageSize).Find(&sharings).Error; err != nil {
		return nil, 0, errors.Wrapf(err, "failed get find sharings")
	}
	return sharings, count, nil
}

func GetSharingsByCreatorId(creator uint, pageIndex, pageSize int) (sharings []model.SharingDB, count int64, err error) {
	sharingDB := db.Model(&model.SharingDB{})
	cond := model.SharingDB{CreatorId: creator}
	if err := sharingDB.Where(cond).Count(&count).Error; err != nil {
		return nil, 0, errors.Wrapf(err, "failed get sharings count")
	}
	if err := sharingDB.Where(cond).Order(columnName("id")).Offset((pageIndex - 1) * pageSize).Limit(pageSize).Find(&sharings).Error; err != nil {
		return nil, 0, errors.Wrapf(err, "failed get find sharings")
	}
	return sharings, count, nil
}

func CreateSharing(s *model.SharingDB) (string, error) {
	if s.ID == "" {
		id := random.String(8)
		for len(id) < 12 {
			old := model.SharingDB{
				ID: id,
			}
			if err := db.Where(old).First(&old).Error; err != nil {
				s.ID = id
				return id, errors.WithStack(db.Create(s).Error)
			}
			id += random.String(1)
		}
		return "", errors.New("failed find valid id")
	} else {
		query := model.SharingDB{ID: s.ID}
		if err := db.Where(query).First(&query).Error; err == nil {
			return "", errors.New("sharing already exist")
		}
		return s.ID, errors.WithStack(db.Create(s).Error)
	}
}

func UpdateSharing(s *model.SharingDB) error {
	return errors.WithStack(db.Save(s).Error)
}

func UpdateSharingId(oldId, newId string) error {
	// Check if new ID already exists
	if err := db.Where("id = ?", newId).First(&model.SharingDB{}).Error; err == nil {
		return errors.New("sharing id already exists")
	}
	return errors.WithStack(db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.SharingDB{}).Where("id = ?", oldId).Update("id", newId).Error; err != nil {
			return err
		}
		return tx.Model(&model.CollectionUpload{}).Where("sharing_id = ?", oldId).Update("sharing_id", newId).Error
	}))
}

func DeleteSharingById(id string) error {
	return errors.WithStack(db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("sharing_id = ?", id).Delete(&model.CollectionUpload{}).Error; err != nil {
			return err
		}
		return tx.Where(model.SharingDB{ID: id}).Delete(&model.SharingDB{}).Error
	}))
}

func DeleteSharingsByCreatorId(creatorId uint) error {
	return errors.WithStack(db.Transaction(func(tx *gorm.DB) error {
		ids := tx.Model(&model.SharingDB{}).Select("id").Where("creator_id = ?", creatorId)
		if err := tx.Where("sharing_id IN (?)", ids).Delete(&model.CollectionUpload{}).Error; err != nil {
			return err
		}
		return tx.Where("creator_id = ?", creatorId).Delete(&model.SharingDB{}).Error
	}))
}

func CreateCollectionUpload(upload *model.CollectionUpload) error {
	return errors.WithStack(db.Create(upload).Error)
}

func GetCollectionUpload(id string) (*model.CollectionUpload, error) {
	upload := model.CollectionUpload{ID: id}
	if err := db.Where(upload).First(&upload).Error; err != nil {
		return nil, errors.WithStack(err)
	}
	return &upload, nil
}

func GetCompletedCollectionUploads(sharingID, visitorHash string) ([]model.CollectionUpload, error) {
	uploads := make([]model.CollectionUpload, 0)
	if err := db.Where("sharing_id = ? AND visitor_hash = ? AND completed = ?", sharingID, visitorHash, true).
		Order("completed_at ASC, id ASC").Find(&uploads).Error; err != nil {
		return nil, errors.WithStack(err)
	}
	return uploads, nil
}

func CompleteCollectionUpload(id, sharingID string) (bool, error) {
	alreadyCompleted := false
	err := db.Transaction(func(tx *gorm.DB) error {
		upload := model.CollectionUpload{}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND sharing_id = ?", id, sharingID).First(&upload).Error; err != nil {
			return err
		}
		if upload.Completed {
			alreadyCompleted = true
			return nil
		}
		if time.Now().After(upload.Expires) {
			return errors.New("collection upload session expired")
		}
		result := tx.Model(&model.SharingDB{}).
			Where("id = ? AND disabled = ? AND (max_accessed = 0 OR accessed < max_accessed)", sharingID, false).
			UpdateColumn("accessed", gorm.Expr("accessed + ?", 1))
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("collection upload limit reached")
		}
		now := time.Now()
		return tx.Model(&upload).Updates(map[string]any{
			"completed":    true,
			"completed_at": &now,
		}).Error
	})
	return alreadyCompleted, errors.WithStack(err)
}

func DeleteCollectionUpload(id string) error {
	return errors.WithStack(db.Where("id = ?", id).Delete(&model.CollectionUpload{}).Error)
}
