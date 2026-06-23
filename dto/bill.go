package dto

type BillingEntitlementRes struct {
	UserID                 int64   `json:"user_id"`
	SubscriptionActive     bool    `json:"subscription_active"`
	CurrentSubscription    string  `json:"current_subscription"`
	SubscriptionExpiredAt  *int64  `json:"subscription_expired_at,omitempty"`
	PointDiscountRate      float64 `json:"point_discount_rate"`
	CanUse1080P            bool    `json:"can_use_1080p"`
	CanUseHDImage          bool    `json:"can_use_hd_image"`
	CanRemoveWatermark     bool    `json:"can_remove_watermark"`
	CanUsePriorityQueue    bool    `json:"can_use_priority_queue"`
	CanUseCustomAspect     bool    `json:"can_use_custom_aspect_ratio"`
	DailyFreeLimit         int     `json:"daily_free_limit"`
	DailyFreeRemain        int     `json:"daily_free_remain"`
	DailyDurationLimitSec  int     `json:"daily_duration_limit_sec"`
	DailyDurationRemainSec int     `json:"daily_duration_remain_sec"`
}

type BillingQuoteReq struct {
	SceneType       string `json:"scene_type" binding:"required"`
	SceneKey        string `json:"scene_key" binding:"required"`
	ResourceType    string `json:"resource_type,omitempty"`
	DurationSeconds int    `json:"duration_seconds,omitempty"`
	Resolution      string `json:"resolution,omitempty"`
	ShotCount       int    `json:"shot_count,omitempty"`
	EnhanceMode     string `json:"enhance_mode,omitempty"`
	Model           string `json:"model,omitempty"`
	Mode            string `json:"mode,omitempty"`
	ImageCount      int    `json:"image_count,omitempty"`
	Quality         string `json:"quality,omitempty"`
	ModelTier       string `json:"model_tier,omitempty"`
	ImageSizeTier   string `json:"image_size_tier,omitempty"`
}

type BillingQuoteRes struct {
	EstimatedPoints    int64                  `json:"estimated_points"`
	PricingSnapshot    map[string]interface{} `json:"pricing_snapshot"`
	EntitlementOK      bool                   `json:"entitlement_ok"`
	InsufficientReason *string                `json:"insufficient_reason,omitempty"`
}
