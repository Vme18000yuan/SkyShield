package application

import (
	"gopkg.in/yaml.v3"
	"strings"
)

func SkillDocumentName(content []byte) string {
	body := string(content)
	if !strings.HasPrefix(body, "---") {
		return ""
	}
	rest := strings.TrimLeft(strings.TrimPrefix(body, "---"), "\r\n")
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return ""
	}
	var meta struct {
		Name string `yaml:"name"`
	}
	if yaml.Unmarshal([]byte(rest[:end]), &meta) != nil {
		return ""
	}
	return strings.TrimSpace(meta.Name)
}
func rewriteSkillMetadata(content []byte, patch SkillMetadataPatch) ([]byte, error) {
	text := string(content)
	if !strings.HasPrefix(text, "---") {
		return nil, ErrInvalidProfile
	}
	start := strings.IndexByte(text, '\n')
	if start < 0 {
		return nil, ErrInvalidProfile
	}
	end := strings.Index(text[start+1:], "\n---")
	if end < 0 {
		return nil, ErrInvalidProfile
	}
	end += start + 1
	bodyStart := end + 4
	var document yaml.Node
	if yaml.Unmarshal([]byte(text[start+1:end]), &document) != nil || len(document.Content) == 0 || document.Content[0].Kind != yaml.MappingNode {
		return nil, ErrInvalidProfile
	}
	mapping := document.Content[0]
	set := func(key string, value any) {
		var node yaml.Node
		_ = node.Encode(value)
		for i := 0; i+1 < len(mapping.Content); i += 2 {
			if mapping.Content[i].Value == key {
				mapping.Content[i+1] = &node
				return
			}
		}
		mapping.Content = append(mapping.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, &node)
	}
	if patch.Description != nil {
		set("description", *patch.Description)
	}
	if patch.License != nil {
		set("license", *patch.License)
	}
	if patch.Compatibility != nil {
		set("compatibility", *patch.Compatibility)
	}
	if patch.MCPs != nil {
		set("mcps", *patch.MCPs)
	}
	header, err := yaml.Marshal(&document)
	if err != nil {
		return nil, err
	}
	return []byte("---\n" + string(header) + "---" + text[bodyStart:]), nil
}
