package agents

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" || seen[trimmed] {
			continue
		}
		seen[trimmed] = true
		result = append(result, trimmed)
	}
	sort.Strings(result)
	return result
}

func hashString(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func shortHash(value string) string {
	hash := hashString(value)
	if len(hash) > 16 {
		return hash[:16]
	}
	return hash
}

func urlHost(value string) string {
	parsed, err := url.Parse(value)
	if err != nil {
		return ""
	}
	return parsed.Host
}

func safeID(prefix string, value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(normalized, "_")
	normalized = strings.Trim(normalized, "_")
	if normalized == "" {
		normalized = shortHash(value)
	}
	if len(normalized) > 48 {
		normalized = normalized[:48]
	}
	return prefix + "_" + normalized
}

type flexibleStringSlice []string

func (s *flexibleStringSlice) UnmarshalJSON(data []byte) error {
	var values []string
	if err := json.Unmarshal(data, &values); err == nil {
		*s = uniqueStrings(values)
		return nil
	}
	var raw []any
	if err := json.Unmarshal(data, &raw); err == nil {
		values = make([]string, 0, len(raw))
		for _, item := range raw {
			if value := stringFromLLMValue(item); value != "" {
				values = append(values, value)
			}
		}
		*s = uniqueStrings(values)
		return nil
	}
	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		*s = splitLLMStringList(single)
		return nil
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	if single = stringFromLLMValue(value); single != "" {
		*s = splitLLMStringList(single)
	}
	return nil
}

func stringSlice(values flexibleStringSlice) []string {
	return []string(values)
}

func stringFromLLMValue(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case float64, bool:
		return strings.TrimSpace(fmt.Sprint(typed))
	case map[string]any:
		for _, key := range []string{"value", "name", "title", "label", "id", "summary", "description"} {
			if candidate, ok := typed[key]; ok {
				if value := stringFromLLMValue(candidate); value != "" {
					return value
				}
			}
		}
	}
	return ""
}

func splitLLMStringList(value string) []string {
	normalized := strings.TrimSpace(value)
	if normalized == "" {
		return nil
	}
	parts := regexp.MustCompile(`[,\n;；、]+`).Split(normalized, -1)
	if len(parts) == 1 {
		return uniqueStrings([]string{normalized})
	}
	return uniqueStrings(parts)
}
