package main

import (
	"errors"
	"sort"
	"strings"
)

func validPublicModel(model string) bool {
	return model != "" && len(model) <= 256 && strings.TrimSpace(model) == model && !strings.ContainsAny(model, "\r\n\t")
}

// Explicit aliases are independent of the original-model checkboxes. Public
// names are unique; multiple public names may refer to the same upstream.
func importPublicModels(models []string, mappings map[string]string) ([]string, error) {
	if len(models)+len(mappings) == 0 || len(models)+len(mappings) > 1024 {
		return nil, errors.New("请选择模型或添加模型重命名")
	}
	seen := map[string]bool{}
	out := []string{}
	for _, model := range models {
		if !validPublicModel(model) || seen[model] {
			return nil, errors.New("对外模型名无效或重复")
		}
		seen[model] = true
		out = append(out, model)
	}
	aliases := []string{}
	for public, upstream := range mappings {
		if !validPublicModel(public) || !validPublicModel(upstream) || seen[public] {
			return nil, errors.New("对外模型名无效或重复，请取消勾选同名模型")
		}
		seen[public] = true
		aliases = append(aliases, public)
	}
	sort.Strings(aliases)
	return append(out, aliases...), nil
}
func importUpstreamModels(models []string, mappings map[string]string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, m := range models {
		if !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	for _, m := range mappings {
		if !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	return out
}
func importModelDefinition(models []string, mappings map[string]string) []any {
	out := []any{}
	for _, m := range models {
		out = append(out, m)
	}
	aliases := []string{}
	for alias := range mappings {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	for _, alias := range aliases {
		out = append(out, map[string]string{mappings[alias]: alias})
	}
	return out
}
func withoutProvider(input []string, provider string) []string {
	out := []string{}
	for _, p := range input {
		if p != provider {
			out = append(out, p)
		}
	}
	return out
}

func validateModelPositions(models []string, positions map[string]int) error {
	allowed := map[string]bool{}
	for _, model := range models {
		allowed[model] = true
	}
	for model, position := range positions {
		if !allowed[model] || position < 1 || position > 1025 {
			return errors.New("模型位置无效，请刷新后重试")
		}
	}
	return nil
}
