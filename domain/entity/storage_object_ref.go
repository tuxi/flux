package entity

import "time"

// StorageObjectRef records explicit references from business entities to assets.
// Used for deletion-safety checks, cleanup scheduling, and access auditing.
type StorageObjectRef struct {
	ID      int64  `gorm:"primaryKey" json:"id"`
	AssetID int64  `gorm:"not null;index:idx_storage_object_refs_asset" json:"asset_id"`
	RefType string `gorm:"type:varchar(64);not null;index:idx_storage_object_refs_ref,priority:1" json:"ref_type"`
	RefID   int64  `gorm:"not null;index:idx_storage_object_refs_ref,priority:2" json:"ref_id"`
	// RefNode is the workflow node name when ref_type is task_node.
	RefNode     string `gorm:"type:varchar(128);not null;default:''" json:"ref_node"`
	Role        string `gorm:"type:varchar(128);not null;default:''" json:"role"`
	OwnerUserID int64  `gorm:"index:idx_storage_object_refs_owner;default:0" json:"owner_user_id"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (StorageObjectRef) TableName() string {
	return "public.storage_object_refs"
}

// Common ref_type values.
const (
	AssetRefTypeTaskInput    = "task_input"
	AssetRefTypeTaskNode     = "task_node"
	AssetRefTypeTaskResult   = "task_result"
	AssetRefTypeCreativeDetail = "creative_detail"
	AssetRefTypeAdminCatalog = "admin_catalog"
)
