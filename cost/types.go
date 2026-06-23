package cost

type ResourceType string

const (
	ResourceTypeTTS             ResourceType = "tts"
	ResourceTypeLLM             ResourceType = "llm"
	ResourceTypeVLM             ResourceType = "vlm"
	ResourceTypeImageGeneration ResourceType = "image_generation"
	ResourceTypeVideoGeneration ResourceType = "video_generation"
)

type UsageFact struct {
	ResourceType      ResourceType
	Provider          string
	Model             string
	ProviderRequestID string
	UsageQuantity     float64
	UsageUnit         string
	UsageBreakdown    map[string]any
	EstimatedCost     float64
	Billable          bool
}

const (
	BillableStageSubmit    = "submit"
	BillableStageCompleted = "completed"
)
