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
	TelegramAppID             int    `json:"telegram_app_id"`
	TelegramAppHash           string `json:"telegram_app_hash"`
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
	APIHost                   string `json:"api_host"`
	APIPort                   int    `json:"api_port"`
	APIToken                  string `json:"api_token"`
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
		TelegramAppID:             c.Telegram.AppID,
		TelegramRPCRetry:          c.Telegram.RpcRetry,
		TelegramMediaGroupTimeout: c.Telegram.MediaGroupTimeout,
		UserbotEnable:             c.Telegram.Userbot.Enable,
		UserbotSession:            c.Telegram.Userbot.Session,
		YtdlpMaxHeight:            c.Ytdlp.MaxHeight,
		YtdlpFormat:               c.Ytdlp.Format,
		YtdlpRecode:               c.Ytdlp.Recode,
		Aria2Enable:               c.Aria2.Enable,
		Aria2URL:                  c.Aria2.Url,
		APIHost:                   c.API.Host,
		APIPort:                   c.API.Port,
	}
}

// SaveProductConfig 定点更新常用配置，未填写的敏感字段保持原值。
func SaveProductConfig(product ProductConfig) error {
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

func (p ProductConfig) validate() error {
	if p.Lang != "zh-Hans" && p.Lang != "en" {
		return errors.New("lang must be zh-Hans or en")
	}
	if p.Workers < 1 || p.Retry < 1 || p.Threads < 1 {
		return errors.New("workers, retry and threads must be at least 1")
	}
	switch p.LogLevel {
	case "debug", "info", "warn", "error", "fatal":
	default:
		return errors.New("invalid log level")
	}
	if p.TelegramAppID < 1 || p.TelegramRPCRetry < 0 || p.TelegramMediaGroupTimeout < 0 {
		return errors.New("invalid Telegram settings")
	}
	if p.UserbotEnable && strings.TrimSpace(p.UserbotSession) == "" {
		return errors.New("userbot session is required when userbot is enabled")
	}
	if p.YtdlpMaxHeight < 0 {
		return errors.New("yt-dlp max height cannot be negative")
	}
	if p.Aria2Enable && strings.TrimSpace(p.Aria2URL) == "" {
		return errors.New("Aria2 URL is required when Aria2 is enabled")
	}
	if strings.TrimSpace(p.APIHost) == "" || p.APIPort < 1 || p.APIPort > 65535 {
		return errors.New("invalid API listen address")
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
