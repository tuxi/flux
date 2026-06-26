package service

import (
	"context"

	"flux/domain/entity"
	"flux/dto"
)

const (
	AssetClassAdminCatalog     = "admin_catalog"
	AssetClassUserUpload       = "user_upload"
	AssetClassTaskIntermediate = "task_intermediate"
	AssetClassTaskResult       = "task_result"
	AssetClassCache            = "cache"
	AssetClassAudit            = "audit"

	AssetVisibilityPublic   = "public"
	AssetVisibilityPrivate  = "private"
	AssetVisibilityInternal = "internal"
	AssetVisibilityProvider = "provider_access"

	AssetOwnerUser   = "user"
	AssetOwnerAdmin  = "admin"
	AssetOwnerSystem = "system"

	AssetSourceClientUpload = "client_upload"
)

type StorageAssetService interface {
	InitUpload(ctx context.Context, userID int64, req *dto.UploadInitReq) (*dto.UploadInitRes, error)
	CompleteUpload(ctx context.Context, userID int64, req *dto.UploadCompleteReq) (*dto.AssetBrief, error)
	ListUserAssets(ctx context.Context, userID int64, req dto.AssetListReq) (*dto.AssetListRes, error)
	GetUserAsset(ctx context.Context, userID int64, assetID int64) (*dto.AssetBrief, error)
	DeleteUserAsset(ctx context.Context, userID int64, assetID int64) (*dto.AssetDeleteRes, error)
	PromoteAsset(ctx context.Context, adminID int64, assetID int64, req *dto.AssetPromoteReq) (*dto.AssetBrief, error)
	ResolveAssetProviderURL(ctx context.Context, userID int64, assetID int64) (string, *entity.StorageObject, error)
	RegisterTaskInputAssetRefs(ctx context.Context, userID int64, taskID int64, input map[string]any) error
	// SignURLsInValue is the legacy recursive URL scanner. Prefer HydrateAssetRefs for new code.
	SignURLsInValue(ctx context.Context, userID int64, value any) any

	// ResolveInternal returns internal asset metadata (oss_key, bucket) for server-side use.
	// Does not generate any signed URL; callers use oss_key with OSS SDK directly.
	ResolveInternal(ctx context.Context, userID int64, assetID int64) (*dto.ResolvedAsset, error)

	// ResolveForProvider generates a signed URL valid for expireSeconds from the call time.
	// Must be called at node execution time, not at task creation time.
	ResolveForProvider(ctx context.Context, userID int64, assetID int64, expireSeconds int64) (*dto.ProviderAsset, error)

	// HydrateAssetRefs recursively scans value for objects containing asset_id and injects
	// url + url_expires_at fields. Replaces SignURLsInValue for new asset-first payloads.
	HydrateAssetRefs(ctx context.Context, userID int64, value any) any

	// RegisterAssetRef explicitly records one asset reference into storage_object_refs.
	RegisterAssetRef(ctx context.Context, ref *dto.AssetRefRecord) error
}
