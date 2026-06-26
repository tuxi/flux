package entity

import (
	"time"

	"gorm.io/gorm"
)

type StorageObjectStatus string

const (
	StorageObjectStatusPending     StorageObjectStatus = "pending"
	StorageObjectStatusActive      StorageObjectStatus = "active"
	StorageObjectStatusDeleting    StorageObjectStatus = "deleting"
	StorageObjectStatusDeleted     StorageObjectStatus = "deleted"
	StorageObjectStatusQuarantined StorageObjectStatus = "quarantined"
)

type StorageObject struct {
	ID int64 `gorm:"primaryKey" json:"id"`

	Bucket string `gorm:"type:varchar(128);not null;uniqueIndex:uk_storage_object_bucket_key,priority:1" json:"bucket"`
	OSSKey string `gorm:"type:text;not null;uniqueIndex:uk_storage_object_bucket_key,priority:2" json:"oss_key"`
	URL    string `gorm:"type:text;not null;default:''" json:"url"`

	OwnerType string `gorm:"type:varchar(32);not null;index:idx_storage_object_owner,priority:1" json:"owner_type"`
	OwnerID   int64  `gorm:"not null;index:idx_storage_object_owner,priority:2" json:"owner_id"`

	AssetClass string `gorm:"type:varchar(32);not null;index" json:"asset_class"`
	AssetKind  string `gorm:"type:varchar(32);not null;index" json:"asset_kind"`
	Visibility string `gorm:"type:varchar(32);not null;index" json:"visibility"`
	Source     string `gorm:"type:varchar(32);not null;index" json:"source"`

	BusinessType string `gorm:"type:varchar(64);not null;default:'';index" json:"business_type"`
	BusinessID   *int64 `gorm:"index" json:"business_id,omitempty"`
	TaskID       *int64 `gorm:"index" json:"task_id,omitempty"`
	NodeName     string `gorm:"type:varchar(128);not null;default:'';index" json:"node_name"`
	UploadID     string `gorm:"type:varchar(128);not null;default:'';index" json:"upload_id"`

	Status           StorageObjectStatus `gorm:"type:varchar(32);not null;default:'pending';index" json:"status"`
	Protect          bool                `gorm:"not null;default:false;index" json:"protect"`
	RefCount         int                 `gorm:"not null;default:0" json:"ref_count"`
	SizeBytes        int64               `gorm:"not null;default:0" json:"size_bytes"`
	ContentType      string              `gorm:"type:varchar(128);not null;default:''" json:"content_type"`
	SHA256           string              `gorm:"type:varchar(128);not null;default:'';index" json:"sha256"`
	OriginalFilename string              `gorm:"type:varchar(255);not null;default:''" json:"original_filename"`
	RetentionUntil   *time.Time          `gorm:"index" json:"retention_until,omitempty"`
	DeleteAttempts   int                 `gorm:"not null;default:0;index" json:"delete_attempts"`
	LastDeleteError  string              `gorm:"type:text;not null;default:''" json:"last_delete_error"`
	LastDeleteAt     *time.Time          `gorm:"index" json:"last_delete_at,omitempty"`

	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"deleted_at"`
}

func (StorageObject) TableName() string {
	return "public.storage_objects"
}
