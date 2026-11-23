package omp

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
)

func canonicalProvider(alias, baseURL string) string {
	// An explicit known endpoint is stronger evidence than a user-defined alias.
	if u, err := url.Parse(baseURL); err == nil {
		switch strings.ToLower(u.Hostname()) {
		case "api.z.ai", "open.bigmodel.cn":
			return "zai"
		case "api.openai.com":
			return "openai"
		case "chatgpt.com", "chat.openai.com":
			return "codex"
		case "api.anthropic.com":
			return "anthropic"
		case "generativelanguage.googleapis.com", "cloudcode-pa.googleapis.com":
			return "gemini"
		case "api.deepseek.com":
			return "deepseek"
		case "api.mistral.ai":
			return "mistral"
		case "api.moonshot.ai", "api.moonshot.cn":
			return "moonshot"
		case "api.kimi.com":
			return "kimi"
		case "openrouter.ai":
			return "openrouter"
		case "api.minimax.io", "api.minimax.chat":
			return "minimax"
		case "api.x.ai":
			return "grok"
		case "api.synthetic.new":
			return "synthetic"
		case "ollama.com":
			return "ollama"
		case "api.commandcode.ai", "api.command-code.ai":
			return "commandcode"
		case "zenmux.ai":
			return "zenmux"
		case "opencode.ai":
			return "opencode"
		case "dashscope.aliyuncs.com", "dashscope-intl.aliyuncs.com", "coding.dashscope.aliyuncs.com", "coding-intl.dashscope.aliyuncs.com":
			return "custom"
		}
	}
	switch strings.ToLower(alias) {
	case "openai-codex", "codex":
		return "codex"
	case "google", "google-gemini-cli", "google-antigravity", "gemini":
		if strings.EqualFold(alias, "google-antigravity") {
			return "antigravity"
		}
		return "gemini"
	case "github-copilot", "github", "copilot":
		return "copilot"
	case "command-code", "commandcode":
		return "commandcode"
	case "z.ai", "zhipu", "zai":
		return "zai"
	case "synthetic", "anthropic", "antigravity", "minimax", "cursor", "grok", "kimi", "moonshot", "deepseek", "openrouter", "mistral", "opencode", "ollama", "muse", "zenmux", "openai", "alibaba":
		return strings.ToLower(alias)
	default:
		return "custom"
	}
}

func makeCredential(id, alias, kind, baseURL string, data map[string]interface{}) (Credential, bool, error) {
	if strings.TrimSpace(alias) == "" {
		return Credential{}, false, errors.New("omp credential provider is empty")
	}
	if baseURL == "" {
		baseURL = stringValue(data, "baseUrl", "baseURL", "base_url")
	}
	if baseURL != "" {
		u, err := url.Parse(baseURL)
		if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return Credential{}, false, errors.New("invalid omp credential base URL")
		}
	}
	kind = strings.ToLower(kind)
	if kind == "" {
		kind = strings.ToLower(stringValue(data, "type", "credential_type"))
	}
	switch kind {
	case "api-key", "apikey", "key":
		kind = "api_key"
	case "oauth", "oauth2", "bearer", "token", "subscription":
		kind = "oauth"
	case "cookie", "cookies", "session":
		kind = "cookie"
	case "api_key":
	default:
		return Credential{}, false, errors.New("unsupported omp credential type")
	}
	provider := canonicalProvider(alias, baseURL)
	values := make(map[string]string)
	mode := "cost"
	if kind == "api_key" {
		key := stringValue(data, "key", "apiKey", "api_key", "token", "value")
		if key == "" {
			return Credential{}, false, nil
		}
		field := "api_key"
		// A Dashscope inference key must never become Alibaba browser cookies.
		if provider == "alibaba" || provider == "codex" || provider == "cursor" || provider == "antigravity" || provider == "kimi" || provider == "copilot" {
			provider = "custom"
		}
		if provider == "zenmux" {
			provider = "custom"
		} // inference keys are not management keys
		values[field] = key
		switch provider {
		case "synthetic", "commandcode", "muse", "opencode":
			mode = "quota"
		case "zai":
			if baseURL == "" || strings.Contains(baseURL, "/coding/") {
				mode = "quota"
			}
		case "minimax":
			if strings.Contains(baseURL, "/coding/") {
				mode = "quota"
			}
		}
	} else if kind == "cookie" {
		cookie := stringValue(data, "cookies", "cookie", "auth_cookie", "value")
		if cookie == "" {
			return Credential{}, false, nil
		}
		switch provider {
		case "alibaba":
			values["cookies"] = cookie
		case "mistral", "opencode":
			values["auth_cookie"] = cookie
		default:
			return Credential{}, false, errors.New("unsupported omp cookie provider")
		}
		mode = "quota"
	} else {
		token := stringValue(data, "access", "accessToken", "access_token", "token")
		refresh := stringValue(data, "refresh", "refreshToken", "refresh_token")
		if token == "" && refresh == "" {
			return Credential{}, false, nil
		}
		if token == "" && provider != "gemini" {
			return Credential{}, false, errors.New("omp OAuth credential has no access token")
		}
		switch provider {
		case "gemini":
			values["access_token"] = token
			if refresh != "" {
				values["refresh_token"] = refresh
			}
		case "antigravity":
			// Remote OAuth is not the local language server's CSRF credential.
			provider = "custom"
			values["api_key"] = token
		case "anthropic", "codex", "copilot", "cursor", "grok", "kimi":
			values["token"] = token
		case "commandcode":
			values["api_key"] = token
		default:
			provider = "custom"
			values["api_key"] = token
		}
		if provider != "custom" {
			mode = "quota"
		}
	}
	for _, key := range []string{"region", "account_id", "workspace_id"} {
		if value := stringValue(data, key); value != "" {
			values[key] = value
		}
	}
	if value := stringValue(data, "accountId"); value != "" {
		values["account_id"] = value
	}
	if provider == "zai" && values["region"] == "" && strings.Contains(baseURL, "open.bigmodel.cn") {
		values["region"] = "cn"
	}
	if provider == "minimax" && values["region"] == "" && strings.Contains(baseURL, "api.minimax.chat") {
		values["region"] = "cn"
	}
	name := alias
	if label := stringValue(data, "email", "name", "label"); label != "" {
		name = alias + " (" + label + ")"
	}
	return Credential{ID: id, Provider: provider, SourceProvider: alias, Name: name, BaseURL: baseURL, Type: kind, Mode: mode, Values: values}, true, nil
}

func readLegacy(ctx context.Context, path string, preferred map[string]bool) ([]Credential, error) {
	b, err := readFile(ctx, path, maxDocumentBytes)
	if err != nil || b == nil {
		return nil, err
	}
	var root interface{}
	if json.Unmarshal(b, &root) != nil {
		return nil, errors.New("invalid legacy omp auth JSON")
	}
	var out []Credential
	count := 0
	var add func(string, string, string, interface{}) error
	add = func(alias, kind, locator string, raw interface{}) error {
		if list, ok := raw.([]interface{}); ok {
			for i, entry := range list {
				if err := add(alias, kind, locator+"/"+strconv.Itoa(i), entry); err != nil {
					return err
				}
			}
			return nil
		}
		count++
		if count > maxCredentials {
			return errors.New("legacy omp credential count exceeds limit")
		}
		data, ok := stringMap(raw)
		if !ok {
			if key, yes := raw.(string); yes && kind == "api_key" {
				data = map[string]interface{}{"key": key}
			} else {
				return errors.New("invalid legacy omp credential")
			}
		}
		if alias == "" {
			alias = stringValue(data, "provider", "providerId", "provider_id")
		}
		if alias == "" {
			return errors.New("legacy omp credential provider is empty")
		}
		if disabled, _ := data["disabled"].(bool); disabled {
			return nil
		}
		if stringValue(data, "disabled_cause", "disabledCause") != "" {
			return nil
		}
		if preferred[alias] {
			return nil
		}
		if kind == "" {
			kind = stringValue(data, "type", "credential_type")
		}
		if kind == "" {
			if stringValue(data, "key", "apiKey", "api_key") != "" {
				kind = "api_key"
			} else {
				kind = "oauth"
			}
		}
		if preferred[alias+"\x00"+kind] {
			return nil
		}
		c, ok, err := makeCredential("legacy:"+locator, alias, kind, "", data)
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("legacy omp credential has no usable credential")
		}
		out = append(out, c)
		return nil
	}
	if list, ok := root.([]interface{}); ok {
		for i, raw := range list {
			if err := add("", "", strconv.Itoa(i), raw); err != nil {
				return nil, err
			}
		}
		return out, nil
	}
	object, ok := stringMap(root)
	if !ok {
		return nil, errors.New("invalid legacy omp auth structure")
	}
	for key, raw := range object {
		if key == "api_key" || key == "oauth" {
			if entries, ok := stringMap(raw); ok {
				for alias, entry := range entries {
					if err := add(alias, key, key+"/"+url.PathEscape(alias), entry); err != nil {
						return nil, err
					}
				}
			} else if err := add("", key, key, raw); err != nil {
				return nil, err
			}
		} else if err := add(key, "", url.PathEscape(key), raw); err != nil {
			return nil, err
		}
	}
	return out, nil
}
