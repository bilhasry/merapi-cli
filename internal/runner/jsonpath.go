package runner

import (
	"fmt"
	"strconv"
	"strings"
)

// jsonPath evaluates a small, predictable subset of JSONPath:
//
//	$.store.book[0].title
//	$.items[*].id
//	$['odd-key'].value
//
// Wildcards are supported for captures (returning a slice); assertions on
// wildcard paths are rejected to avoid ambiguous comparisons.
func jsonPath(data any, path string) (any, error) {
	segments, err := parsePath(path)
	if err != nil {
		return nil, err
	}
	cur := []any{data}
	for _, seg := range segments {
		var next []any
		for _, node := range cur {
			vals, err := applySegment(node, seg)
			if err != nil {
				return nil, err
			}
			next = append(next, vals...)
		}
		cur = next
		if len(cur) == 0 {
			return nil, nil // not found
		}
	}
	if len(cur) == 1 && !lastWasWildcard(segments) {
		return cur[0], nil
	}
	if lastWasWildcard(segments) {
		return cur, nil
	}
	return cur, nil
}

func lastWasWildcard(segments []pathSegment) bool {
	return len(segments) > 0 && segments[len(segments)-1].wildcard
}

type pathSegment struct {
	key      string
	index    int
	isIndex  bool
	wildcard bool
}

func parsePath(path string) ([]pathSegment, error) {
	if path == "" || path[0] != '$' {
		return nil, fmt.Errorf("jsonpath must start with '$', got %q", path)
	}
	var segments []pathSegment
	i := 1
	for i < len(path) {
		switch path[i] {
		case '.':
			i++
			start := i
			for i < len(path) && path[i] != '.' && path[i] != '[' {
				i++
			}
			if start == i {
				return nil, fmt.Errorf("empty segment in jsonpath %q", path)
			}
			segments = append(segments, pathSegment{key: path[start:i]})
		case '[':
			end := strings.IndexByte(path[i:], ']')
			if end < 0 {
				return nil, fmt.Errorf("unclosed '[' in jsonpath %q", path)
			}
			inner := path[i+1 : i+end]
			i += end + 1
			if inner == "*" {
				segments = append(segments, pathSegment{wildcard: true})
				continue
			}
			if len(inner) >= 2 && (inner[0] == '\'' || inner[0] == '"') && inner[len(inner)-1] == inner[0] {
				segments = append(segments, pathSegment{key: inner[1 : len(inner)-1]})
				continue
			}
			n, err := strconv.Atoi(inner)
			if err != nil {
				return nil, fmt.Errorf("unsupported bracket %q in jsonpath %q (use [n], [*] or ['key'])", inner, path)
			}
			segments = append(segments, pathSegment{index: n, isIndex: true})
		default:
			return nil, fmt.Errorf("unexpected %q in jsonpath %q (expected . or [)", string(path[i]), path)
		}
	}
	return segments, nil
}

func applySegment(node any, seg pathSegment) ([]any, error) {
	if seg.wildcard {
		switch t := node.(type) {
		case []any:
			return t, nil
		case map[string]any:
			var vals []any
			var keys []string
			for k := range t {
				keys = append(keys, k)
			}
			sortStrings(keys)
			for _, k := range keys {
				vals = append(vals, t[k])
			}
			return vals, nil
		default:
			return nil, nil
		}
	}
	if seg.isIndex {
		arr, ok := node.([]any)
		if !ok {
			return nil, nil
		}
		if seg.index < 0 || seg.index >= len(arr) {
			return nil, nil
		}
		return []any{arr[seg.index]}, nil
	}
	m, ok := node.(map[string]any)
	if !ok {
		return nil, nil
	}
	v, ok := m[seg.key]
	if !ok {
		return nil, nil
	}
	return []any{v}, nil
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
