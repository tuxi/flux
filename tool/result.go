package tool

type Result struct {
	Success bool           `json:"success"`
	Data    map[string]any `json:"data,omitempty"`
	Error   string         `json:"error,omitempty"`
}

func Success(data map[string]any) *Result {
	return &Result{
		Success: true,
		Data:    data,
	}
}

func Fail(err error) *Result {
	return &Result{
		Success: false,
		Error:   err.Error(),
	}
}
