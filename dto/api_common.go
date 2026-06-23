package dto

// ApiResponse 统一响应结构
type ApiResponse struct {
	TraceID string      `json:"trace_id"`              // 与日志系统对齐
	Code    int         `json:"code" example:"200"`    // 业务错误码
	Message string      `json:"msg" example:"success"` // 提示信息
	Data    interface{} `json:"data"`                  // 数据负载
}

// SecurityToolReq 用于测试加密解密工具的请求结构
type SecurityToolReq struct {
	Text   string `json:"text" binding:"required"`
	Action string `json:"action" binding:"required,oneof=encrypt decrypt"` // encrypt 或 decrypt
}

type SecurityToolRes struct {
	Action string `json:"action"`
	Result string `json:"result"`
}
