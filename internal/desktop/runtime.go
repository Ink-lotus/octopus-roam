// Package desktop provides the native Windows launcher used by the .cmd menu.
package desktop

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const stateName = ".octopus-roam-process.json"

type State struct {
	PID     int    `json:"pid"`
	Created uint64 `json:"created"`
	Ready   bool   `json:"ready"`
}

func Directory(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := canonicalPath(absolute)
	if err != nil {
		return "", fmt.Errorf("解析运行目录: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("运行目录不存在")
	}
	return filepath.Clean(resolved), nil
}

func Identity(directory string) string {
	sum := sha256.Sum256([]byte(strings.ToUpper(filepath.Clean(directory))))
	return strings.ToUpper(hex.EncodeToString(sum[:]))
}

// WriteJSON publishes a complete file; readers never observe a partial record.
func WriteJSON(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".octopus-record-*")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if _, err = file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

func prepare(directory string) error {
	if info, err := os.Stat(filepath.Join(directory, "octopus-roam.exe")); err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("缺少 octopus-roam.exe，请完整解压 Windows 压缩包")
	}
	for _, name := range []string{"data", "logs", "backups"} {
		if err := os.MkdirAll(filepath.Join(directory, name), 0o755); err != nil {
			return err
		}
	}
	path := filepath.Join(directory, "data", "config.json")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if os.IsExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	_, writeErr := file.WriteString("{\n  \"server\": {\"host\": \"127.0.0.1\", \"port\": 8080},\n  \"database\": {\"type\": \"sqlite\", \"path\": \"data/data.db\"},\n  \"log\": {\"level\": \"info\"}\n}\n")
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

func Endpoint(directory string) (string, string, error) {
	data, err := os.ReadFile(filepath.Join(directory, "data", "config.json"))
	if err != nil {
		return "", "", err
	}
	var config map[string]json.RawMessage
	if json.Unmarshal(data, &config) != nil || config == nil {
		return "", "", fmt.Errorf("data/config.json 必须是有效的 JSON 对象")
	}
	var server struct {
		Host *string         `json:"host"`
		Port json.RawMessage `json:"port"`
	}
	if value, ok := config["server"]; ok && json.Unmarshal(value, &server) != nil {
		return "", "", fmt.Errorf("server 配置无效")
	}
	host, port := "0.0.0.0", "8080"
	if server.Host != nil {
		host = *server.Host
	}
	if len(server.Port) != 0 {
		port = string(server.Port)
		if strings.HasPrefix(port, "\"") && json.Unmarshal(server.Port, &port) != nil {
			return "", "", fmt.Errorf("server.port 无效")
		}
	}
	if override := os.Getenv("OCTOPUS_ROAM_SERVER_HOST"); override != "" {
		host = override
	}
	if override := os.Getenv("OCTOPUS_ROAM_SERVER_PORT"); override != "" {
		port = override
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return "", "", fmt.Errorf("server.port 必须为 1 到 65535")
	}
	return host, strconv.Itoa(number), nil
}

func AccessURL(directory string) (string, error) {
	host, port, err := Endpoint(directory)
	if err != nil {
		return "", err
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port), nil
}
