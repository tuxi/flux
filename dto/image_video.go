package dto

// ImageToVideoInput 图片生成视频生成核心配置
type ImageToVideoInput struct {
	// 必选参数
	ImageURL    string `json:"image_url"`    // 上游上传的图片OSS URL
	Prompt      string `json:"prompt"`       // 提示词（如："夏日海滩，阳光，海浪，动态镜头，4K"）
	Model       string `json:"model"`        // 大模型选择（如：runway-gen2/pika-v1/aliyun-v1）
	Duration    int    `json:"duration"`     // 时长（秒）：1-30s
	Resolution  string `json:"resolution"`   // 分辨率：720p/1080p/2K/4K
	AspectRatio string `json:"aspect_ratio"` // 画面比例：16:9（横屏）/9:16（竖屏）/1:1（方形）

	// 可选参数（提升效果）
	NegativePrompt string `json:"negative_prompt"` // 反向提示词（如："模糊，卡顿，水印"）
	Style          string `json:"style"`           // 风格：写实/动漫/国风/赛博朋克
	FPS            int    `json:"fps"`             // 帧率：24/30/60（默认30）
	APIProvider    string `json:"api_provider"`    // API服务商：aliyun/baidu/runway/pika
}
