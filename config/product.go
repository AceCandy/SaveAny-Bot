package config

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/pelletier/go-toml/v2/unstable"
)

// ProductConfig 是 Web 产品配置页管理的常用配置。
type ProductConfig struct {
	Lang                      string `json:"lang"`
	Workers                   int    `json:"workers"`
	Retry                     int    `json:"retry"`
	Threads                   int    `json:"threads"`
	Stream                    bool   `json:"stream"`
	LogLevel                  string `json:"log_level"`
	TelegramToken             string `json:"telegram_token"`
	TelegramTokenConfigured   bool   `json:"telegram_token_configured"`
	TelegramAppID             int    `json:"telegram_app_id"`
	TelegramAppHash           string `json:"telegram_app_hash"`
	TelegramAppHashConfigured bool   `json:"telegram_app_hash_configured"`
	TelegramRPCRetry          int    `json:"telegram_rpc_retry"`
	TelegramMediaGroupTimeout int    `json:"telegram_media_group_timeout"`
	UserbotEnable             bool   `json:"userbot_enable"`
	UserbotSession            string `json:"userbot_session"`
	YtdlpMaxHeight            int    `json:"ytdlp_max_height"`
	YtdlpFormat               string `json:"ytdlp_format"`
	YtdlpRecode               string `json:"ytdlp_recode"`
	Aria2Enable               bool   `json:"aria2_enable"`
	Aria2URL                  string `json:"aria2_url"`
	Aria2Secret               string `json:"aria2_secret"`
	Aria2SecretConfigured     bool   `json:"aria2_secret_configured"`
	APIHost                   string `json:"api_host"`
	APIPort                   int    `json:"api_port"`
	APIToken                  string `json:"api_token"`
	APITokenConfigured        bool   `json:"api_token_configured"`
}

// ReadProductConfig 返回当前有效配置，敏感值留空表示不在页面回显。
func ReadProductConfig() ProductConfig {
	c := C()
	return ProductConfig{
		Lang:                      c.Lang,
		Workers:                   c.Workers,
		Retry:                     c.Retry,
		Threads:                   c.Threads,
		Stream:                    c.Stream,
		LogLevel:                  c.Log.Level,
		TelegramTokenConfigured:   c.Telegram.Token != "",
		TelegramAppID:             c.Telegram.AppID,
		TelegramAppHashConfigured: c.Telegram.AppHash != "",
		TelegramRPCRetry:          c.Telegram.RpcRetry,
		TelegramMediaGroupTimeout: c.Telegram.MediaGroupTimeout,
		UserbotEnable:             c.Telegram.Userbot.Enable,
		UserbotSession:            c.Telegram.Userbot.Session,
		YtdlpMaxHeight:            c.Ytdlp.MaxHeight,
		YtdlpFormat:               c.Ytdlp.Format,
		YtdlpRecode:               c.Ytdlp.Recode,
		Aria2Enable:               c.Aria2.Enable,
		Aria2URL:                  c.Aria2.Url,
		Aria2SecretConfigured:     c.Aria2.Secret != "",
		APIHost:                   c.API.Host,
		APIPort:                   c.API.Port,
		APITokenConfigured:        c.API.Token != "",
	}
}

// SaveProductConfig 定点更新常用配置，未填写的敏感字段保持原值。
func SaveProductConfig(product ProductConfig) error {
	if strings.TrimSpace(product.TelegramToken) == "" && strings.TrimSpace(C().Telegram.Token) == "" {
		return errors.New("Telegram Bot Token 不能为空")
	}
	if err := product.validate(); err != nil {
		return err
	}
	updates := []productConfigValue{
		{"", "lang", product.Lang},
		{"", "workers", product.Workers},
		{"", "retry", product.Retry},
		{"", "threads", product.Threads},
		{"", "stream", product.Stream},
		{"log", "level", product.LogLevel},
		{"telegram", "app_id", product.TelegramAppID},
		{"telegram", "rpc_retry", product.TelegramRPCRetry},
		{"telegram", "media_group_timeout", product.TelegramMediaGroupTimeout},
		{"telegram.userbot", "enable", product.UserbotEnable},
		{"telegram.userbot", "session", product.UserbotSession},
		{"ytdlp", "max_height", product.YtdlpMaxHeight},
		{"ytdlp", "format", product.YtdlpFormat},
		{"ytdlp", "recode", product.YtdlpRecode},
		{"aria2", "enable", product.Aria2Enable},
		{"aria2", "url", product.Aria2URL},
		{"api", "host", product.APIHost},
		{"api", "port", product.APIPort},
	}
	for _, secret := range []productConfigValue{
		{"telegram", "token", product.TelegramToken},
		{"telegram", "app_hash", product.TelegramAppHash},
		{"aria2", "secret", product.Aria2Secret},
		{"api", "token", product.APIToken},
	} {
		if secret.value != "" {
			updates = append(updates, secret)
		}
	}
	return updateManagedConfig(func(original []byte) ([]byte, error) {
		updated, err := patchProductTOML(original, updates)
		if err != nil {
			return nil, err
		}
		if err := ValidateTOML(updated); err != nil {
			return nil, fmt.Errorf("validate config file: %w", err)
		}
		return updated, nil
	})
}

// DisableUserbot 在注销个人账号前关闭 Userbot，避免重启后再次自动登录。
func DisableUserbot() error {
	return updateManagedConfig(func(original []byte) ([]byte, error) {
		updated, err := patchProductTOML(original, []productConfigValue{{"telegram.userbot", "enable", false}})
		if err != nil {
			return nil, err
		}
		if err := ValidateTOML(updated); err != nil {
			return nil, fmt.Errorf("validate config file: %w", err)
		}
		return updated, nil
	})
}

func (p ProductConfig) validate() error {
	if p.Lang != "zh-Hans" && p.Lang != "en" {
		return errors.New("语言必须为简体中文或 English")
	}
	if p.Workers < 1 || p.Retry < 1 || p.Threads < 1 {
		return errors.New("并行任务数、失败重试次数和单任务线程数必须至少为 1")
	}
	switch p.LogLevel {
	case "debug", "info", "warn", "error", "fatal":
	default:
		return errors.New("日志级别无效")
	}
	if p.TelegramAppID < 1 || p.TelegramRPCRetry < 0 || p.TelegramMediaGroupTimeout < 0 {
		return errors.New("Telegram App ID 必须大于 0，重试次数和相册等待时间不能为负数")
	}
	if p.UserbotEnable && strings.TrimSpace(p.UserbotSession) == "" {
		return errors.New("启用 Userbot 时必须填写会话文件位置")
	}
	if p.YtdlpMaxHeight < 0 {
		return errors.New("yt-dlp 最高分辨率不能为负数")
	}
	if p.Aria2Enable && strings.TrimSpace(p.Aria2URL) == "" {
		return errors.New("启用 Aria2 时必须填写 RPC 地址")
	}
	if strings.TrimSpace(p.APIHost) == "" || p.APIPort < 1 || p.APIPort > 65535 {
		return errors.New("Web API 监听地址不能为空，端口必须在 1 到 65535 之间")
	}
	return nil
}

type productConfigValue struct {
	section string
	key     string
	value   any
}

type productSection struct {
	end int
}

// patchProductTOML 只替换受管字段的值，并在缺失时补入对应表。
func patchProductTOML(data []byte, updates []productConfigValue) ([]byte, error) {
	literals := make(map[string][]byte, len(updates))
	for _, update := range updates {
		literal, err := marshalTOMLLiteral(update.value)
		if err != nil {
			return nil, fmt.Errorf("encode %s: %w", productConfigPath(update), err)
		}
		literals[productConfigPath(update)] = literal
	}

	sections := map[string]productSection{"": {end: len(data)}}
	found := make(map[string]bool, len(updates))
	replacements := make([]tomlReplacement, 0, len(updates))
	currentSection := ""
	var parser unstable.Parser
	parser.Reset(data)
	for parser.NextExpression() {
		node := parser.Expression()
		switch node.Kind {
		case unstable.Table, unstable.ArrayTable:
			section, offset := productNodeKey(node)
			if current, ok := sections[currentSection]; ok {
				current.end = lineStart(data, offset)
				sections[currentSection] = current
			}
			if node.Kind == unstable.Table {
				currentSection = section
				sections[currentSection] = productSection{end: len(data)}
			} else {
				currentSection = "\x00"
			}
		case unstable.KeyValue:
			key, _ := productNodeKey(node)
			path := key
			if currentSection != "" {
				path = currentSection + "." + key
			}
			if literal, ok := literals[path]; ok {
				replacements = append(replacements, tomlReplacement{node.Value().Raw, literal})
				found[path] = true
			}
		}
	}
	if err := parser.Error(); err != nil {
		return nil, fmt.Errorf("parse config file: %w", err)
	}

	missing := make(map[string][]string)
	sectionOrder := make([]string, 0)
	for _, update := range updates {
		path := productConfigPath(update)
		if found[path] {
			continue
		}
		if _, ok := missing[update.section]; !ok {
			sectionOrder = append(sectionOrder, update.section)
		}
		missing[update.section] = append(missing[update.section], update.key+" = "+string(literals[path]))
	}
	for _, section := range sectionOrder {
		lines := strings.Join(missing[section], "\n") + "\n"
		if span, ok := sections[section]; ok {
			replacements = append(replacements, tomlReplacement{unstable.Range{Offset: uint32(span.end)}, []byte(lines)})
			continue
		}
		prefix := "\n[" + section + "]\n"
		if len(data) == 0 || data[len(data)-1] == '\n' {
			prefix = "[" + section + "]\n"
		}
		replacements = append(replacements, tomlReplacement{unstable.Range{Offset: uint32(len(data))}, []byte(prefix + lines + "\n")})
	}
	sort.SliceStable(replacements, func(i, j int) bool { return replacements[i].raw.Offset < replacements[j].raw.Offset })
	return replaceTOMLRanges(data, replacements), nil
}

func productConfigPath(value productConfigValue) string {
	if value.section == "" {
		return value.key
	}
	return value.section + "." + value.key
}

func productNodeKey(node *unstable.Node) (string, int) {
	parts := make([]string, 0, 2)
	offset := 0
	keys := node.Key()
	for keys.Next() {
		key := keys.Node()
		if len(parts) == 0 {
			offset = int(key.Raw.Offset)
		}
		parts = append(parts, string(key.Data))
	}
	return strings.Join(parts, "."), offset
}

func lineStart(data []byte, offset int) int {
	for offset > 0 && data[offset-1] != '\n' {
		offset--
	}
	return offset
}

func marshalTOMLLiteral(value any) ([]byte, error) {
	encoded, err := toml.Marshal(map[string]any{"value": value})
	if err != nil {
		return nil, err
	}
	line := bytes.TrimSpace(encoded)
	literal, ok := bytes.CutPrefix(line, []byte("value = "))
	if !ok {
		return nil, errors.New("unexpected TOML encoding")
	}
	return literal, nil
}
