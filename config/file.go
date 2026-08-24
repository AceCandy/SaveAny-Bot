package config

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/pelletier/go-toml/v2/unstable"
	"github.com/spf13/viper"
)

const (
	redactedValuePrefix = "__SAVEANY_REDACTED_"
	maxConfigFileSize   = 2 << 20
)

var managedConfig = struct {
	sync.RWMutex
	source string
	remote bool
}{}

var managedConfigFileMu sync.Mutex

// ManagedConfigFile is the local TOML file currently used by the process.
type ManagedConfigFile struct {
	Content  string
	Source   string
	ReadOnly bool
}

func setConfigSource(source string, remote bool) error {
	if source == "" {
		return errors.New("config source is unknown")
	}
	if !remote {
		resolved, err := filepath.EvalSymlinks(source)
		if err != nil {
			return fmt.Errorf("resolve config file: %w", err)
		}
		source, err = filepath.Abs(resolved)
		if err != nil {
			return fmt.Errorf("resolve config file path: %w", err)
		}
	}
	managedConfig.Lock()
	managedConfig.source = source
	managedConfig.remote = remote
	managedConfig.Unlock()
	return nil
}

func configSource() (string, bool) {
	managedConfig.RLock()
	defer managedConfig.RUnlock()
	return managedConfig.source, managedConfig.remote
}

// ReadManagedConfig returns a redacted copy suitable for the admin dashboard.
func ReadManagedConfig() (ManagedConfigFile, error) {
	source, remote := configSource()
	if source == "" {
		return ManagedConfigFile{}, errors.New("config source is unknown")
	}
	if remote {
		return ManagedConfigFile{Source: source, ReadOnly: true}, nil
	}

	managedConfigFileMu.Lock()
	defer managedConfigFileMu.Unlock()
	data, err := readConfigFile(source)
	if err != nil {
		return ManagedConfigFile{}, err
	}
	redacted, err := redactTOML(data)
	if err != nil {
		return ManagedConfigFile{}, fmt.Errorf("redact config file: %w", err)
	}
	return ManagedConfigFile{Content: string(redacted), Source: source}, nil
}

// SaveManagedConfig validates and replaces the active local TOML file.
func SaveManagedConfig(content string) error {
	return updateManagedConfig(func(original []byte) ([]byte, error) {
		return prepareTOML(original, []byte(content))
	})
}

func updateManagedConfig(update func([]byte) ([]byte, error)) error {
	source, remote := configSource()
	if source == "" {
		return errors.New("config source is unknown")
	}
	if remote {
		return errors.New("remote config sources are read-only")
	}

	managedConfigFileMu.Lock()
	defer managedConfigFileMu.Unlock()
	original, err := readConfigFile(source)
	if err != nil {
		return err
	}
	data, err := update(original)
	if err != nil {
		return err
	}
	info, err := os.Stat(source)
	if err != nil {
		return fmt.Errorf("stat config file: %w", err)
	}
	return replaceConfigFile(source, data, original, info.Mode().Perm())
}

func readConfigFile(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat config file: %w", err)
	}
	if info.Size() > maxConfigFileSize {
		return nil, fmt.Errorf("config file exceeds %d bytes", maxConfigFileSize)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}
	return data, nil
}

func redactTOML(data []byte) ([]byte, error) {
	replacements := make([]tomlReplacement, 0)
	index := 0
	err := visitTOMLStrings(data, func(key string, value *unstable.Node) error {
		if !isSensitiveKey(key) && !hasURLPassword(string(value.Data)) {
			return nil
		}
		index++
		placeholder := redactedValue(index)
		replacements = append(replacements, tomlReplacement{value.Raw, strconv.AppendQuote(nil, placeholder)})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return replaceTOMLRanges(data, replacements), nil
}

func isSensitiveKey(key string) bool {
	key = strings.ToLower(strings.ReplaceAll(key, "-", "_"))
	for _, marker := range []string{"password", "passwd", "secret", "token", "cookie", "credential", "authorization", "access_key", "private_key"} {
		if strings.Contains(key, marker) {
			return true
		}
	}
	return key == "app_hash" || key == "api_key" || key == "apikey"
}

func hasURLPassword(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil || parsed.User == nil {
		return false
	}
	_, ok := parsed.User.Password()
	return ok
}

func prepareTOML(original, submitted []byte) ([]byte, error) {
	secrets := make(map[string][]byte)
	index := 0
	if err := visitTOMLStrings(original, func(key string, value *unstable.Node) error {
		if isSensitiveKey(key) || hasURLPassword(string(value.Data)) {
			index++
			secrets[redactedValue(index)] = append([]byte(nil), original[value.Raw.Offset:value.Raw.Offset+value.Raw.Length]...)
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("parse current config file: %w", err)
	}

	replacements := make([]tomlReplacement, 0, len(secrets))
	if err := visitTOMLStrings(submitted, func(_ string, value *unstable.Node) error {
		placeholder := string(value.Data)
		if !isRedactedValue(placeholder) {
			return nil
		}
		secret, ok := secrets[placeholder]
		if !ok {
			return fmt.Errorf("unknown redacted value %q", placeholder)
		}
		replacements = append(replacements, tomlReplacement{value.Raw, secret})
		return nil
	}); err != nil {
		return nil, fmt.Errorf("parse submitted config file: %w", err)
	}
	data := replaceTOMLRanges(submitted, replacements)
	if err := ValidateTOML(data); err != nil {
		return nil, fmt.Errorf("validate config file: %w", err)
	}
	return data, nil
}

type tomlReplacement struct {
	raw   unstable.Range
	value []byte
}

func visitTOMLStrings(data []byte, visit func(string, *unstable.Node) error) error {
	var parser unstable.Parser
	parser.Reset(data)
	for parser.NextExpression() {
		if err := visitTOMLNode(parser.Expression(), visit); err != nil {
			return err
		}
	}
	return parser.Error()
}

func visitTOMLNode(node *unstable.Node, visit func(string, *unstable.Node) error) error {
	if node.Kind == unstable.KeyValue {
		var key string
		keys := node.Key()
		for keys.Next() {
			key = string(keys.Node().Data)
		}
		value := node.Value()
		if value.Kind == unstable.String {
			if err := visit(key, value); err != nil {
				return err
			}
		}
		return visitTOMLNode(value, visit)
	}
	if node.Kind != unstable.Array && node.Kind != unstable.InlineTable {
		return nil
	}
	children := node.Children()
	for children.Next() {
		if err := visitTOMLNode(children.Node(), visit); err != nil {
			return err
		}
	}
	return nil
}

func replaceTOMLRanges(data []byte, replacements []tomlReplacement) []byte {
	result := append([]byte(nil), data...)
	for index := len(replacements) - 1; index >= 0; index-- {
		replacement := replacements[index]
		start := int(replacement.raw.Offset)
		end := start + int(replacement.raw.Length)
		result = append(result[:start], append(replacement.value, result[end:]...)...)
	}
	return result
}

func redactedValue(index int) string {
	return redactedValuePrefix + strconv.Itoa(index) + "__"
}

func isRedactedValue(value string) bool {
	if !strings.HasPrefix(value, redactedValuePrefix) || !strings.HasSuffix(value, "__") {
		return false
	}
	_, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(value, redactedValuePrefix), "__"))
	return err == nil
}

// ValidateTOML applies the same decoding and storage validation used at startup.
func ValidateTOML(data []byte) error {
	v := viper.New()
	setupViper(v)
	setDefaults(v)
	if err := v.ReadConfig(bytes.NewReader(data)); err != nil {
		return err
	}
	_, err := decodeConfig(v)
	return err
}

func replaceConfigFile(path string, data, original []byte, mode os.FileMode) error {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, "."+filepath.Base(path)+"-*")
	if err != nil {
		return fmt.Errorf("create temporary config file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(mode); err != nil {
		temporary.Close()
		return fmt.Errorf("set temporary config permissions: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return fmt.Errorf("write temporary config file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync temporary config file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary config file: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err == nil {
		syncDirectory(directory)
		return nil
	} else if !errors.Is(err, syscall.EBUSY) {
		return fmt.Errorf("replace config file: %w", err)
	}

	// 单文件 bind mount 不能被 rename 替换；同步覆盖以兼容现有 Compose 挂载。
	if err := overwriteConfigFile(path, data, mode); err != nil {
		_ = overwriteConfigFile(path, original, mode)
		return err
	}
	return nil
}

func overwriteConfigFile(path string, data []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("open mounted config file: %w", err)
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return fmt.Errorf("write mounted config file: %w", err)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("sync mounted config file: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close mounted config file: %w", err)
	}
	return nil
}

func syncDirectory(path string) {
	directory, err := os.Open(path)
	if err == nil {
		_ = directory.Sync()
		_ = directory.Close()
	}
}
