package utils

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"gorm.io/gorm/utils"
)

func ParseFinal(data []byte) (map[string]any, error) {

	if len(data) == 0 {
		return nil, fmt.Errorf("empty output")
	}

	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}

	final, ok := out["final"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("final not found")
	}

	return final, nil
}

func ToAnySlice(val any) ([]any, bool) {
	if val == nil {
		return nil, false
	}
	// 如果本来就是数组any就返回
	switch v := val.(type) {
	case []any:
		return v, true
	// 字符串、int、float包装为数组
	case string, int, float64:

		return []any{v}, true
	}

	rv := reflect.ValueOf(val)
	if rv.Kind() != reflect.Slice {
		return nil, false
	}

	n := rv.Len()
	res := make([]any, n)
	for i := 0; i < n; i++ {
		res[i] = rv.Index(i).Interface()
	}
	return res, true
}

func ToInt(v any) int {
	return int(ToInt64(v))
}

func ToInt64(v any) int64 {
	switch val := v.(type) {
	case int:
		return int64(val)
	case int32:
		return int64(val)
	case int64:
		return val
	case float64:
		return int64(val)
	case string:
		i, _ := strconv.ParseInt(val, 10, 64)
		return i
	default:
		return 0
	}
}

func ToFloat64(v any) float64 {
	switch val := v.(type) {
	case float32:
		return float64(val)
	case float64:
		return val
	case int64:
		return float64(val)
	case int32:
		return float64(val)
	case int:
		return float64(val)
	case string:
		i, _ := strconv.ParseFloat(val, 64)
		return i
	default:
		return 0
	}
}

func ToBool(v any) bool {
	switch val := v.(type) {
	case bool:
		return val
	case string:
		return val == "true"
	case int:
		return val == 1
	case float64:
		return val == 1
	default:
		return false
	}
}

//func ToString(v any) string {
//	switch val := v.(type) {
//	case nil:
//		return ""
//	case string:
//		return val
//	case []byte:
//		return string(val)
//	case map[string]any, []any: // 拦截常见的 JSON 容器类型
//		b, _ := json.Marshal(val)
//		return string(b)
//	default:
//		// 如果是 struct 或其他 map 类型，也可以尝试 json.Marshal
//		// 这里提供一个通用的逻辑：
//		b, err := json.Marshal(val)
//		if err == nil {
//			return string(b)
//		}
//		return fmt.Sprintf("%v", val)
//	}
//}

func ToString(v any) string {
	// 1. 极速路径：处理最常用的基础类型 (不涉及反射)
	switch val := v.(type) {
	case nil:
		return ""
	case string:
		return val
	case []byte:
		return string(val)
	case int:
		return strconv.Itoa(val)
	case bool:
		return strconv.FormatBool(val)
	}

	// 2. 动态路径：处理复杂类型 (使用反射)
	rv := reflect.ValueOf(v)
	kind := rv.Kind()

	// 如果是指针，递归拿到底层值
	if kind == reflect.Ptr {
		if rv.IsNil() {
			return ""
		}
		return ToString(rv.Elem().Interface())
	}

	// 针对需要 JSON 的类型进行拦截
	if kind == reflect.Map || kind == reflect.Slice || kind == reflect.Struct {
		b, err := json.Marshal(v)
		if err == nil {
			return string(b)
		}
	}

	// 3. 最终兜底
	return fmt.Sprintf("%v", v)
}

func NormalizeStringSlice(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		key := normalizeKey(s)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, s)
	}
	return out
}

func NormalizeSellingPoints(in []string) []string {
	return NormalizeStringSlice(in)
}

func normalizeKey(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// ObjectToMap 把 对象转换为字典
func ObjectToMap(data any) (map[string]any, error) {
	b, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("json marshal failed: %w", err)
	}

	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("json unmarshal failed: %w", err)
	}

	return out, nil
}

func ContainsAny(s string, values ...string) bool {
	lower := strings.ToLower(strings.TrimSpace(s))
	for _, v := range values {
		if strings.Contains(lower, strings.ToLower(strings.TrimSpace(v))) {
			return true
		}
	}
	return false
}

func EmptyAs(v string, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

func SanitizeJSONBlock(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	return strings.TrimSpace(s)
}

func GetMapString(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func FilterNonEmpty(values []string) []string {
	result := make([]string, 0, len(values))
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v != "" {
			result = append(result, v)
		}
	}
	return result
}

func ToStringSlice(v any) []string {
	switch vv := v.(type) {
	case []string:
		return NormalizeStringSlice(vv)
	case []any:
		out := make([]string, 0, len(vv))
		for _, item := range vv {
			s := strings.TrimSpace(utils.ToString(item))
			if s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func ToMap(v any) map[string]any {
	if v == nil {
		return nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	return m
}

func ToSlice(v any) []any {
	if v == nil {
		return nil
	}
	arr, ok := v.([]any)
	if !ok {
		return []any{v}
	}
	return arr
}

func FirstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// IsSlice 判断任意值是否为切片类型（兼容所有切片）
func IsSlice(val any) bool {
	if val == nil {
		return false
	}
	// 通过反射判断是否为切片
	rv := reflect.ValueOf(val)
	return rv.Kind() == reflect.Slice
}

func IsObject(val any) bool {
	if val == nil {
		return false
	}

	if _, ok := val.(map[string]any); ok {
		return true
	}

	rv := reflect.ValueOf(val)
	rt := reflect.TypeOf(val)

	// 指针先解引用
	for rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return false
		}
		rv = rv.Elem()
		rt = rt.Elem()
	}

	switch rv.Kind() {
	case reflect.Map:
		// 允许 map[string]T
		return rt.Key().Kind() == reflect.String
	case reflect.Struct:
		return true
	default:
		return false
	}
}

func ValueOrEmpty(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func CloneStringMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// NormalizeAnyMap 将 map[string]any 做递归规范化，保证 hash 稳定
func NormalizeAnyMap(src map[string]any) map[string]any {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = NormalizeAny(v)
	}
	return dst
}

func NormalizeAny(v any) any {
	switch x := v.(type) {
	case map[string]any:
		return NormalizeAnyMap(x)
	case []any:
		out := make([]any, 0, len(x))
		for _, item := range x {
			out = append(out, NormalizeAny(item))
		}
		return out
	default:
		return x
	}
}

func NormalizeMap(m map[string]any) map[string]any {

	res := make(map[string]any)

	keys := make([]string, 0, len(m))

	for k := range m {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	for _, k := range keys {

		v := m[k]

		switch vv := v.(type) {

		case map[string]any:
			res[k] = NormalizeMap(vv)

		case []any:
			res[k] = NormalizeSlice(vv)

		default:
			res[k] = v
		}
	}

	return res
}

func NormalizeSlice(arr []any) []any {

	res := make([]any, len(arr))

	for i, v := range arr {

		switch vv := v.(type) {

		case map[string]any:
			res[i] = NormalizeMap(vv)

		case []any:
			res[i] = NormalizeSlice(vv)

		default:
			res[i] = v
		}
	}

	return res
}

func CompactStrings(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		s := strings.TrimSpace(item)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

func StringPtr(str string) *string {
	if str == "" {
		return nil
	}
	return &str
}

// NewCommand 创建带上下文的命令
// 作用：统一封装 ffmpeg / ffprobe 等外部命令，支持 ctx 取消、安全拼接、日志打印
func NewCommand(ctx context.Context, name string, args ...string) *exec.Cmd {
	// 1. 使用上下文创建命令（支持超时/取消）
	cmd := exec.CommandContext(ctx, name, args...)

	// 2. 继承环境变量（保证 ffmpeg 等工具正常运行）
	cmd.Env = os.Environ()

	// 3. 可选：日志打印执行命令（调试定位问题）
	cmdLine := name + " " + strings.Join(args, " ")
	Infof("utils.NewCommand: %s", cmdLine)

	return cmd
}

func Infof(format string, args ...interface{}) {
	fmt.Printf("[INFO] "+format, args...)
}

func Errorf(format string, args ...interface{}) {
	fmt.Printf("[ERROR] "+format, args...)
}
