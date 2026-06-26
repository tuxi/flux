package domain

import "encoding/json"

type CreativeDetail struct {
	Version      string           `json:"version"`
	WorkflowName string           `json:"workflow_name"`
	Mode         string           `json:"mode"`
	Summary      *CreativeSummary `json:"summary,omitempty"`

	Input         CreativeDetailBlock `json:"input"`
	Understanding CreativeDetailBlock `json:"understanding"`
	Output        CreativeDetailBlock `json:"output"`
}

type CreativeSummary struct {
	Title       string   `json:"title,omitempty"`
	Subtitle    string   `json:"subtitle,omitempty"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}

type CreativeDetailBlock struct {
	Title    string                  `json:"title"`
	Sections []CreativeDetailSection `json:"sections"`
}

type CreativeSectionType string

const (
	CreativeSectionTypeText        CreativeSectionType = "text"
	CreativeSectionTypeKV          CreativeSectionType = "kv"
	CreativeSectionTypeTags        CreativeSectionType = "tags"
	CreativeSectionTypeList        CreativeSectionType = "list"
	CreativeSectionTypeCard        CreativeSectionType = "card"
	CreativeSectionTypeGallery     CreativeSectionType = "gallery"
	CreativeSectionTypeScript      CreativeSectionType = "script"
	CreativeSectionTypeMedia       CreativeSectionType = "media"
	CreativeSectionTypeShotResults CreativeSectionType = "shot_results"
)

type CreativeDetailSection struct {
	Key     string              `json:"key"`
	Title   string              `json:"title"`
	Type    CreativeSectionType `json:"type"`
	Payload any                 `json:"payload"`
}

type KVItem struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Value any    `json:"value"`
}

type KVPayload struct {
	Items []KVItem `json:"items"`
}

type TextPayload struct {
	Text string `json:"text"`
}

type TagsPayload struct {
	Items []string `json:"items"`
}

type MediaItem struct {
	URL       string `json:"url"`
	CoverURL  string `json:"cover_url,omitempty"`
	Type      string `json:"type,omitempty"`
	Role      string `json:"role,omitempty"`
	Title     string `json:"title,omitempty"`
	Thumbnail string `json:"thumbnail,omitempty"`
}

type GalleryPayload struct {
	Items []MediaItem `json:"items"`
}

type ScriptShot struct {
	Index        int      `json:"index"`
	Title        string   `json:"title,omitempty"`
	Description  string   `json:"description,omitempty"`
	VisualPrompt string   `json:"visual_prompt,omitempty"`
	Voiceover    string   `json:"voiceover,omitempty"`
	Duration     float64  `json:"duration,omitempty"`
	Tags         []string `json:"tags,omitempty"`
}

type ScriptPayload struct {
	Shots []ScriptShot `json:"shots"`
}

type MediaPayload struct {
	Type       string `json:"type"`
	URL        string `json:"url,omitempty"`
	PreviewURL string `json:"preview_url,omitempty"`
	CoverURL   string `json:"cover_url,omitempty"`
	// AssetID is set when URL is unavailable (e.g. asset-id-only clients).
	// The frontend resolves the display URL via GET /api/v1/assets/:id.
	AssetID  int64   `json:"asset_id,omitempty"`
	Width    int64   `json:"width,omitempty"`
	Height   int64   `json:"height,omitempty"`
	Duration float64 `json:"duration,omitempty"`
}

// GeneratedShotDetail 分镜详情
type GeneratedShotDetail struct {
	Index int `json:"index"`

	// 脚本侧
	Title        string   `json:"title,omitempty"`
	Description  string   `json:"description,omitempty"`
	VisualPrompt string   `json:"visual_prompt,omitempty"`
	Voiceover    string   `json:"voiceover,omitempty"`
	Duration     float64  `json:"duration,omitempty"`
	Tags         []string `json:"tags,omitempty"`

	// 实际执行结果侧
	VideoURL        string  `json:"video_url,omitempty"`
	CoverURL        string  `json:"cover_url,omitempty"`
	TailFrameURL    string  `json:"tail_frame_url,omitempty"`
	ShotType        string  `json:"shot_type,omitempty"`
	TransitionMode  string  `json:"transition_mode,omitempty"`
	FrameSource     string  `json:"frame_source,omitempty"`
	PrimarySource   string  `json:"primary_source,omitempty"`
	ReferencePack   string  `json:"reference_pack,omitempty"`
	SelectionReason string  `json:"selection_reason,omitempty"`
	PlannedDuration float64 `json:"planned_duration,omitempty"`
	APITaskID       string  `json:"api_task_id,omitempty"`
}

type ShotResultsPayload struct {
	Items []GeneratedShotDetail `json:"items"`
}

func ParseCreativeDetail(d any) (*CreativeDetail, error) {
	var creativeDetail CreativeDetail
	switch d.(type) {
	case map[string]interface{}:
		bytes, err := json.Marshal(d)
		if err != nil {
			return nil, err
		}
		err = json.Unmarshal(bytes, &creativeDetail)
		if err != nil {
			return nil, err
		}
	case CreativeDetail:
		creativeDetail = d.(CreativeDetail)
	case *CreativeDetail:
		if d, ok := d.(*CreativeDetail); ok {
			creativeDetail = *d
		}
	}
	return &creativeDetail, nil
}
