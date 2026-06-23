package consts

import "time"

const (
	// UserID 用户id key
	UserID = "user_id"
	// AccountType gin.Context 中存放的账号类型（anonymous / registered）
	AccountType = "account_type"
	// 用于 Context 追踪
	TraceIDKey = "trace_id"
)

const (
	CBCKEY                = "EBPOEACAESCAEBCS"
	RegisterAnonymousSlat = "objc.com.anonymous"
)

const (
	LanguageId     = "X-Language-Id"
	DeviceType     = "X-Device-Type"
	DeviceName     = "X-Device-Name"
	OSVersion      = "X-OS_Version"
	ClientId       = "X-App-Id"
	ClientVersion  = "X-App-Version"
	DeviceId       = "X-Device-ID"
	Timestamp      = "X-Timestamp"
	Signature      = "X-Signature"
	UserInfoPrefix = "User_Info_list_2:"
	DeviceInfo     = "device_info"
)

const (
	// 默认redis过期时间
	RedisExrDefault = time.Hour * 24 * 5
)

// 角色定义
const (
	Guest        = 1
	StandardUser = iota + 1
	VIPMember
	SVIPMember = 301
	Admin      = 5090
)

var RoleToString = map[int]string{
	Guest:        "Guest", // 游客，不存在数据库，没有 token
	StandardUser: "User",  // 普通用户
	VIPMember:    "ViIP",  // 历史兼容角色，不作为会员状态真相
	SVIPMember:   "SVIP",  // 历史兼容角色
	Admin:        "Admin", // 管理员
}

const (
	CaptchaPrefix        = "Captchat_list:"
	UserDeviceInfoPrefix = "User_Device_info:2"
	JWTTokenCtx          = "token_ctx"
)
