package response

import (
	dto2 "flux/dto"
	"flux/internal/consts"
	"net/http"

	"github.com/gin-gonic/gin"
)

// Result 基础返回逻辑
func Result(c *gin.Context, httpStatus int, code int, msg string, data interface{}) {
	// 自动从 Context 获取 TraceID
	traceID := c.GetString(consts.TraceIDKey)

	c.JSON(httpStatus, dto2.ApiResponse{
		TraceID: traceID,
		Code:    code,
		Message: msg,
		Data:    data,
	})
}

// Success 成功返回 (200 OK)
func Success(c *gin.Context, data interface{}) {
	Result(c, http.StatusOK, 0, "success", data)
}

// Error 业务错误返回 (400 Bad Request)
func Error(c *gin.Context, code int, msg string) {
	Result(c, http.StatusBadRequest, code, msg, nil)
}

// ErrorWithStatus 返回指定 HTTP 状态码的错误响应
func ErrorWithStatus(c *gin.Context, httpStatus int, code int, msg string) {
	Result(c, httpStatus, code, msg, nil)
}

// WriteError 根据 error 自动映射 HTTP 状态码和业务错误码
//func WriteError(c *gin.Context, err error) {
//	if err == nil {
//		return
//	}
//
//	switch {
//	case errors.Is(err, context.Canceled):
//		// 499 is a widely used non-standard status for client-closed requests.
//		c.Status(499)
//	case errors.Is(err, context.DeadlineExceeded):
//		Result(c, http.StatusGatewayTimeout, consts.Unknown, "request timeout", nil)
//	case errors.Is(err, gorm.ErrRecordNotFound):
//		Result(c, http.StatusNotFound, consts.NotFoundErr, "not found", nil)
//	default:
//		Result(c, http.StatusInternalServerError, consts.Unknown, err.Error(), nil)
//	}
//}

// Unauthorized 鉴权失败 (401)
func Unauthorized(c *gin.Context, msg string) {
	if msg == "" {
		msg = "登录已过期，请重新登录"
	}
	Result(c, http.StatusUnauthorized, 401, msg, nil)
}

// Forbidden 权限不足 (403)
func Forbidden(c *gin.Context) {
	Result(c, http.StatusForbidden, 403, "您没有权限执行此操作", nil)
}
