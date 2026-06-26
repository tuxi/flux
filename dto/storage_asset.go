package dto

import "time"

type UploadInitReq struct {
	AssetClass   string `json:"asset_class" binding:"required"`
	AssetKind    string `json:"asset_kind" binding:"required"`
	BusinessType string `json:"business_type"`
	Filename     string `json:"filename" binding:"required"`
	ContentType  string `json:"content_type"`
	SizeBytes    int64  `json:"size_bytes"`
}

type UploadInitRes struct {
	AssetID   int64       `json:"asset_id"`
	UploadID  string      `json:"upload_id"`
	Bucket    string      `json:"bucket"`
	Region    string      `json:"region"`
	Endpoint  string      `json:"endpoint"`
	Host      string      `json:"host"`
	Dir       string      `json:"dir"`
	ObjectKey string      `json:"object_key"`
	STS       STSInfo     `json:"sts"`
	Asset     *AssetBrief `json:"asset,omitempty"`
}

type STSInfo struct {
	AccessKeyID     string `json:"access_key_id"`
	AccessKeySecret string `json:"access_key_secret"`
	SecurityToken   string `json:"security_token"`
	Expiration      string `json:"expiration"`
}

type UploadCompleteReq struct {
	AssetID  int64  `json:"asset_id" binding:"required"`
	UploadID string `json:"upload_id" binding:"required"`
	OSSKey   string `json:"oss_key" binding:"required"`
}

type AssetListReq struct {
	AssetClass string `form:"asset_class"`
	AssetKind  string `form:"asset_kind"`
	Page       int    `form:"page"`
	PageSize   int    `form:"page_size"`
}

type AssetListRes struct {
	Items    []*AssetBrief `json:"items"`
	Page     int           `json:"page"`
	PageSize int           `json:"page_size"`
	Total    int64         `json:"total"`
}

type AssetBrief struct {
	AssetID        int64      `json:"asset_id"`
	URL            string     `json:"url"`
	URLExpiresAt   *time.Time `json:"url_expires_at,omitempty"`
	RetentionUntil *time.Time `json:"retention_until,omitempty"`
	RetentionDays  int64      `json:"retention_days,omitempty"`
	OSSKey         string     `json:"oss_key,omitempty"`
	Bucket         string     `json:"bucket,omitempty"`
	AssetClass     string     `json:"asset_class"`
	AssetKind      string     `json:"asset_kind"`
	Visibility     string     `json:"visibility"`
	Filename       string     `json:"filename"`
	SizeBytes      int64      `json:"size_bytes"`
	ContentType    string     `json:"content_type"`
	Status         string     `json:"status"`
	CanDelete      bool       `json:"can_delete"`
	RefCount       int        `json:"ref_count"`
	CreatedAt      time.Time  `json:"created_at"`
}

type AssetDeleteRes struct {
	AssetID    int64  `json:"asset_id"`
	DeleteMode string `json:"delete_mode"`
	Status     string `json:"status"`
	Message    string `json:"message,omitempty"`
}

type AssetPromoteReq struct {
	BusinessType string `json:"business_type" binding:"required"`
	BusinessID   *int64 `json:"business_id"`
}

// ResolvedAsset carries internal asset metadata returned by AssetResolver.
// Used for server-side OSS access; never returned to the client directly.
type ResolvedAsset struct {
	AssetID    int64
	OSSKey     string
	Bucket     string
	AssetKind  string
	AssetClass string
	Visibility string
	OwnerType  string
	OwnerID    int64
}

// AssetRefRecord is the input for registering an explicit asset reference.
type AssetRefRecord struct {
	AssetID     int64
	RefType     string
	RefID       int64
	RefNode     string
	Role        string
	OwnerUserID int64
}

// ProviderAsset is the output of ResolveForProvider.
type ProviderAsset struct {
	AssetID     int64
	OSSKey      string
	ProviderURL string
	ExpiresAt   *time.Time
}

// HydratedAssetRef is a single asset_id that has been enriched with a signed URL.
type HydratedAssetRef struct {
	AssetID    int64      `json:"asset_id"`
	URL        string     `json:"url"`
	ExpiresAt  *time.Time `json:"url_expires_at,omitempty"`
	AssetKind  string     `json:"asset_kind,omitempty"`
	AssetClass string     `json:"asset_class,omitempty"`
}
