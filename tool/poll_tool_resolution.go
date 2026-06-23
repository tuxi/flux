package tool

import "strings"

var preferredPollToolAliases = map[string]string{
	"aliyun_image_generate_wait":     "aliyun_image_generate_poll_once",
	"aliyun_image_to_image_wait":     "aliyun_image_to_image_poll_once",
	"volcengine_image_generate_wait": "volcengine_image_generate_poll_once",
	"video_generate_wait":            "video_generate_poll_once",
	"goods_shot_i2v_wait":            "goods_shot_i2v_poll_once",
}

func ResolvePreferredPollTool(reg *Registry, requested string) (string, Tool, bool) {
	name := strings.TrimSpace(requested)
	if reg == nil || name == "" {
		return "", nil, false
	}

	if alias := strings.TrimSpace(preferredPollToolAliases[name]); alias != "" {
		if toolImpl, ok := reg.Get(alias); ok {
			return alias, toolImpl, true
		}
	}

	toolImpl, ok := reg.Get(name)
	if !ok {
		return "", nil, false
	}
	return name, toolImpl, true
}
