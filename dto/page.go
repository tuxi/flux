package dto

type PageRequest struct {
	Page     int    `form:"page" json:"page" validate:"min=1"`                   // 校验页码≥1
	PageSize int    `form:"page_size" json:"page_size" validate:"min=1,max=100"` // 每页≤100
	Sort     string `form:"sort" json:"sort"`
	Order    string `form:"order" json:"order" validate:"oneof=asc desc"` // 只能是 asc/desc
}

func (p *PageRequest) Offset() int {
	if p.Page <= 1 {
		return 0
	}
	return (p.Page - 1) * p.PageSize
}

func (p *PageRequest) GetLimit() int {
	if p.PageSize <= 0 {
		return 20 // 默认每页20条
	}
	return p.PageSize
}
