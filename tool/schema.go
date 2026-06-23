package tool

// DataSchema 数据契约模型
type DataSchema struct {
	Fields map[string]FieldSchema
}

type FieldSchema struct {
	Type     string // string | number | object | array
	Required bool
	Desc     string
}
