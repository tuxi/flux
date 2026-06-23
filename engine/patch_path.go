package engine

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

type pathTokenType int

const (
	pathTokenKey pathTokenType = iota
	pathTokenIndex
)

type pathToken struct {
	Type  pathTokenType
	Key   string
	Index int
}

func (t pathToken) IsIndex() bool {
	return t.Type == pathTokenIndex
}

func (t pathToken) IsKey() bool {
	return t.Type == pathTokenKey
}

// parsePatchTokens 生产级 path 解析规则：
//   - foo.bar.baz      => key("foo"), key("bar"), key("baz")
//   - foo[0].bar       => key("foo"), index(0), key("bar")
//   - foo["0"].bar     => key("foo"), key("0"), key("bar")
//   - foo['bar'].baz   => key("foo"), key("bar"), key("baz")
//
// 关键规则：
//   - dot 后的纯数字也只是 key，不再自动视为 index
//   - 只有 [0] 这种 bracket 数字形式才是 index
func parsePatchTokens(path string) ([]pathToken, error) {
	path = strings.TrimSpace(path)
	path = strings.Trim(path, ".")
	if path == "" {
		return nil, nil
	}

	var tokens []pathToken
	i := 0
	n := len(path)

	for i < n {
		switch path[i] {
		case '.':
			i++
			continue

		case '[':
			tok, next, err := parseBracketToken(path, i)
			if err != nil {
				return nil, err
			}
			tokens = append(tokens, tok)
			i = next

		default:
			start := i
			for i < n && path[i] != '.' && path[i] != '[' {
				i++
			}
			part := strings.TrimSpace(path[start:i])
			if part == "" {
				return nil, fmt.Errorf("invalid empty path segment in %q", path)
			}
			tokens = append(tokens, pathToken{
				Type: pathTokenKey,
				Key:  part,
			})
		}
	}

	return tokens, nil
}

func parseBracketToken(path string, start int) (pathToken, int, error) {
	// path[start] == '['
	i := start + 1
	n := len(path)

	for i < n && unicode.IsSpace(rune(path[i])) {
		i++
	}
	if i >= n {
		return pathToken{}, 0, fmt.Errorf("unterminated bracket segment in %q", path)
	}

	// ["foo"] or ['foo']
	if path[i] == '"' || path[i] == '\'' {
		quote := path[i]
		i++

		var sb strings.Builder
		closed := false
		for i < n {
			ch := path[i]
			if ch == '\\' {
				if i+1 >= n {
					return pathToken{}, 0, fmt.Errorf("invalid escape in bracket string of %q", path)
				}
				sb.WriteByte(path[i+1])
				i += 2
				continue
			}
			if ch == quote {
				i++
				closed = true
				break
			}
			sb.WriteByte(ch)
			i++
		}
		if !closed {
			return pathToken{}, 0, fmt.Errorf("unterminated quoted key in %q", path)
		}

		for i < n && unicode.IsSpace(rune(path[i])) {
			i++
		}
		if i >= n || path[i] != ']' {
			return pathToken{}, 0, fmt.Errorf("missing closing ] in %q", path)
		}
		i++

		return pathToken{
			Type: pathTokenKey,
			Key:  sb.String(),
		}, i, nil
	}

	// [0]
	numStart := i
	for i < n && path[i] != ']' {
		i++
	}
	if i >= n {
		return pathToken{}, 0, fmt.Errorf("missing closing ] in %q", path)
	}

	raw := strings.TrimSpace(path[numStart:i])
	i++ // skip ]

	if raw == "" {
		return pathToken{}, 0, fmt.Errorf("empty bracket token in %q", path)
	}

	idx, err := strconv.Atoi(raw)
	if err != nil {
		return pathToken{}, 0, fmt.Errorf("invalid bracket token %q in %q: only numeric index or quoted key is allowed", raw, path)
	}
	if idx < 0 {
		return pathToken{}, 0, fmt.Errorf("negative array index: %d", idx)
	}

	return pathToken{
		Type:  pathTokenIndex,
		Index: idx,
	}, i, nil
}

// splitPatchPath 仅保留兼容；真实 patch 逻辑不要依赖它。
// dot 永远按 key 处理。
func splitPatchPath(path string) []string {
	path = strings.TrimSpace(path)
	path = strings.Trim(path, ".")
	if path == "" {
		return nil
	}

	raw := strings.Split(path, ".")
	out := make([]string, 0, len(raw))
	for _, p := range raw {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}

// ensureObjectPath 仅兼容纯 object 路径。
// bracket path 不应再调用它。
func ensureObjectPath(root map[string]any, parts []string) (map[string]any, string, error) {
	if root == nil {
		return nil, "", fmt.Errorf("root is nil")
	}
	if len(parts) == 0 {
		return nil, "", fmt.Errorf("path is empty")
	}
	if len(parts) == 1 {
		return root, parts[0], nil
	}

	cur := root
	for i := 0; i < len(parts)-1; i++ {
		key := parts[i]
		if key == "" {
			return nil, "", fmt.Errorf("invalid empty path segment at index %d", i)
		}
		if strings.Contains(key, "[") || strings.Contains(key, "]") {
			return nil, "", fmt.Errorf("ensureObjectPath does not support bracket path segment %q", key)
		}

		existing, ok := cur[key]
		if !ok || existing == nil {
			next := map[string]any{}
			cur[key] = next
			cur = next
			continue
		}

		nextMap, ok := existing.(map[string]any)
		if !ok {
			return nil, "", fmt.Errorf("path segment %q is not an object", key)
		}
		cur = nextMap
	}

	return cur, parts[len(parts)-1], nil
}

func GetByPath(root map[string]any, path string) (any, bool) {
	if root == nil {
		return nil, false
	}

	tokens, err := parsePatchTokens(path)
	if err != nil {
		return nil, false
	}
	if len(tokens) == 0 {
		return root, true
	}

	var cur any = root
	for _, tok := range tokens {
		switch node := cur.(type) {
		case map[string]any:
			if tok.IsIndex() {
				return nil, false
			}
			v, ok := node[tok.Key]
			if !ok {
				return nil, false
			}
			cur = v

		case []any:
			if !tok.IsIndex() {
				return nil, false
			}
			if tok.Index < 0 || tok.Index >= len(node) {
				return nil, false
			}
			cur = node[tok.Index]

		default:
			return nil, false
		}
	}

	return cur, true
}

func SetByPath(root map[string]any, path string, value any) error {
	if root == nil {
		return fmt.Errorf("root is nil")
	}

	tokens, err := parsePatchTokens(path)
	if err != nil {
		return err
	}
	if len(tokens) == 0 {
		return fmt.Errorf("path is empty")
	}

	return setAtAny(root, tokens, value)
}

func setAtAny(cur any, tokens []pathToken, value any) error {
	if len(tokens) == 0 {
		return nil
	}

	tok := tokens[0]
	last := len(tokens) == 1

	switch node := cur.(type) {
	case map[string]any:
		if tok.IsIndex() {
			return fmt.Errorf("path segment [%d] expects array but got object", tok.Index)
		}

		if last {
			node[tok.Key] = value
			return nil
		}

		child, ok := node[tok.Key]
		if !ok || child == nil {
			child = makeContainerForNext(tokens[1])
			node[tok.Key] = child
		}

		return setAtAny(child, tokens[1:], value)

	case []any:
		if !tok.IsIndex() {
			return fmt.Errorf("path segment %q expects object but got array", tok.Key)
		}
		if tok.Index < 0 {
			return fmt.Errorf("negative array index: %d", tok.Index)
		}
		if tok.Index >= len(node) {
			return fmt.Errorf("array index out of range: %d", tok.Index)
		}

		if last {
			node[tok.Index] = value
			return nil
		}

		child := node[tok.Index]
		if child == nil {
			child = makeContainerForNext(tokens[1])
			node[tok.Index] = child
		}

		return setAtAny(child, tokens[1:], value)

	default:
		return fmt.Errorf("intermediate path container is neither object nor array")
	}
}

func DeleteByPath(root map[string]any, path string) error {
	if root == nil {
		return fmt.Errorf("root is nil")
	}

	tokens, err := parsePatchTokens(path)
	if err != nil {
		return err
	}
	if len(tokens) == 0 {
		return fmt.Errorf("path is empty")
	}

	return deleteAtAny(root, tokens)
}

func deleteAtAny(cur any, tokens []pathToken) error {
	if len(tokens) == 0 {
		return nil
	}

	tok := tokens[0]
	last := len(tokens) == 1

	switch node := cur.(type) {
	case map[string]any:
		if tok.IsIndex() {
			return fmt.Errorf("path segment [%d] expects array but got object", tok.Index)
		}

		if last {
			delete(node, tok.Key)
			return nil
		}

		child, ok := node[tok.Key]
		if !ok || child == nil {
			return nil
		}

		return deleteAtAny(child, tokens[1:])

	case []any:
		if !tok.IsIndex() {
			return fmt.Errorf("path segment %q expects object but got array", tok.Key)
		}
		if tok.Index < 0 || tok.Index >= len(node) {
			return nil
		}

		if last {
			node[tok.Index] = nil
			return nil
		}

		child := node[tok.Index]
		if child == nil {
			return nil
		}

		return deleteAtAny(child, tokens[1:])

	default:
		return nil
	}
}

func MergeByPath(root map[string]any, path string, value any) error {
	if root == nil {
		return fmt.Errorf("root is nil")
	}

	src, ok := value.(map[string]any)
	if !ok {
		return fmt.Errorf("merge value must be map[string]any, got %T", value)
	}

	if strings.TrimSpace(path) == "" {
		for k, v := range src {
			root[k] = v
		}
		return nil
	}

	existing, ok := GetByPath(root, path)
	if !ok || existing == nil {
		return SetByPath(root, path, deepCloneMap(src))
	}

	dst, ok := existing.(map[string]any)
	if !ok {
		return fmt.Errorf("merge target at path %q is not an object", path)
	}

	for k, v := range src {
		dst[k] = v
	}
	return nil
}

func makeContainerForNext(next pathToken) any {
	if next.IsIndex() {
		return []any{}
	}
	return map[string]any{}
}
