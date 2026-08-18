package config

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ParseImportKV 解析配置导入文本：JSON 对象（与导出相同）或 key=value 每行。
// 行格式支持 # 注释；值里的 \n 会转成换行。
func ParseImportKV(data []byte) (map[string]string, error) {
	s := strings.TrimSpace(string(data))
	if s == "" {
		return nil, fmt.Errorf("内容为空")
	}
	if strings.HasPrefix(s, "{") {
		return parseImportJSON(data)
	}
	return parseImportLines(s)
}

func parseImportJSON(data []byte) (map[string]string, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("JSON 解析失败: %w", err)
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("没有可导入的配置项")
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		k = strings.TrimSpace(k)
		if k == "" {
			return nil, fmt.Errorf("存在空 key")
		}
		var val string
		if err := json.Unmarshal(v, &val); err != nil {
			return nil, fmt.Errorf("key %q 的值必须是字符串", k)
		}
		out[k] = val
	}
	return out, nil
}

func parseImportLines(s string) (map[string]string, error) {
	out := map[string]string{}
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		i := strings.Index(line, "=")
		if i <= 0 {
			return nil, fmt.Errorf("无效行: %q", line)
		}
		key := strings.TrimSpace(line[:i])
		if key == "" {
			return nil, fmt.Errorf("无效行: %q", line)
		}
		val := strings.TrimSpace(line[i+1:])
		val = strings.ReplaceAll(val, `\n`, "\n")
		out[key] = val
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("没有可导入的配置项")
	}
	return out, nil
}
