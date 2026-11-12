// Generic helpers that read typed values out of a row's loosely typed field map.

package importservice

import (
	"fmt"
	"strconv"
	"strings"
)

func (s *Service) extractStrings(fields map[string]interface{}, fieldName string) []string {
	if fieldName == "" {
		return nil
	}
	value, ok := fields[fieldName]
	if !ok || value == nil {
		return nil
	}

	switch v := value.(type) {
	case []string:
		result := make([]string, 0, len(v))
		for _, item := range v {
			item = strings.TrimSpace(item)
			if item != "" {
				result = append(result, item)
			}
		}
		return result
	case []interface{}:
		result := make([]string, 0, len(v))
		for _, item := range v {
			itemStr := strings.TrimSpace(fmt.Sprint(item))
			if itemStr != "" {
				result = append(result, itemStr)
			}
		}
		return result
	case string:
		parts := strings.Split(v, ",")
		result := make([]string, 0, len(parts))
		for _, item := range parts {
			item = strings.TrimSpace(item)
			if item != "" {
				result = append(result, item)
			}
		}
		return result
	default:
		item := strings.TrimSpace(fmt.Sprint(v))
		if item == "" {
			return nil
		}
		return []string{item}
	}
}

func cloneMap(source map[string]interface{}) map[string]interface{} {
	cloned := make(map[string]interface{}, len(source))
	for key, value := range source {
		cloned[key] = cloneValue(value)
	}
	return cloned
}

func cloneValue(value interface{}) interface{} {
	switch v := value.(type) {
	case map[string]interface{}:
		return cloneMap(v)
	case []interface{}:
		cloned := make([]interface{}, len(v))
		for i := range v {
			cloned[i] = cloneValue(v[i])
		}
		return cloned
	default:
		return v
	}
}

// extractString extracts a string value from fields
func (s *Service) extractString(fields map[string]interface{}, fieldName string) string {
	if fieldName == "" {
		return ""
	}
	if val, ok := fields[fieldName]; ok {
		return strings.TrimSpace(fmt.Sprint(val))
	}
	return ""
}

// extractInt extracts an integer value from fields
func (s *Service) extractInt(fields map[string]interface{}, fieldName string) int {
	if fieldName == "" {
		return 0
	}
	if val, ok := fields[fieldName]; ok {
		switch v := val.(type) {
		case int:
			return v
		case int64:
			return int(v)
		case float64:
			return int(v)
		case string:
			if i, err := strconv.Atoi(v); err == nil {
				return i
			}
		}
	}
	return 0
}

// extractBool extracts a boolean value from fields
func (s *Service) extractBool(fields map[string]interface{}, fieldName string) bool {
	if fieldName == "" {
		return false
	}
	if val, ok := fields[fieldName]; ok {
		switch v := val.(type) {
		case bool:
			return v
		case string:
			return strings.EqualFold(v, "true") || strings.EqualFold(v, "yes") || strings.EqualFold(v, "1")
		case int:
			return v != 0
		case float64:
			return v != 0
		}
	}
	return false
}

// extractTags extracts tags from fields
func (s *Service) extractTags(fields map[string]interface{}, fieldName string) []string {
	if fieldName == "" {
		return nil
	}
	if val, ok := fields[fieldName]; ok {
		switch v := val.(type) {
		case []interface{}:
			tags := make([]string, 0, len(v))
			for _, item := range v {
				tag := strings.TrimSpace(fmt.Sprint(item))
				if tag != "" {
					tags = append(tags, tag)
				}
			}
			return tags
		case []string:
			return v
		case string:
			// Parse comma-separated tags
			parts := strings.Split(v, ",")
			tags := make([]string, 0, len(parts))
			for _, part := range parts {
				tag := strings.TrimSpace(part)
				if tag != "" {
					tags = append(tags, tag)
				}
			}
			return tags
		}
	}
	return nil
}
