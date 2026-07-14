package agents

import (
	"regexp"
	"strings"
)

var (
	chinesePasswordTextPattern = regexp.MustCompile(`(?i)(密码|口令)\s*[:：=]?\s*([^\s,，。;；)）]{4,})`)
	englishPasswordTextPattern = regexp.MustCompile(`(?i)\b(password|passwd|pwd|passcode)\b\s*[:：=]\s*([^\s,，。;；)）]{4,})`)
	chineseUsernameTextPattern = regexp.MustCompile(`(?i)(账号|账户|用户名|邮箱)\s*[:：=]?\s*([a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,})`)
	englishUsernameTextPattern = regexp.MustCompile(`(?i)\b(email|username|user)\b\s*[:：=]\s*([a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,})`)
	bearerTextPattern          = regexp.MustCompile(`(?i)bearer\s+[a-z0-9._\-]{12,}`)
	apiKeyTextPattern          = regexp.MustCompile(`(?i)\b(sk-[a-z0-9]{12,}|api[_-]?key\s*[:：=]\s*[^\s,，。;；)）]{8,})`)
)

func RedactSensitiveUserText(value string) string {
	if strings.TrimSpace(value) == "" {
		return value
	}
	redacted := chineseUsernameTextPattern.ReplaceAllString(value, "$1[secret_ref:local-dev/demo_username]")
	redacted = englishUsernameTextPattern.ReplaceAllString(redacted, "$1=[secret_ref:local-dev/demo_username]")
	redacted = chinesePasswordTextPattern.ReplaceAllString(redacted, "[secret_ref:local-dev/demo_password]")
	redacted = englishPasswordTextPattern.ReplaceAllString(redacted, "[secret_ref:local-dev/demo_password]")
	redacted = bearerTextPattern.ReplaceAllString(redacted, "Bearer [secret_ref:redacted_token]")
	redacted = apiKeyTextPattern.ReplaceAllString(redacted, "[secret_ref:redacted_api_key]")
	return redacted
}

func RedactSensitiveUserTexts(values []string) []string {
	if len(values) == 0 {
		return values
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, RedactSensitiveUserText(value))
	}
	return out
}
