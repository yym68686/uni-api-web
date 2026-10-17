package main

import "testing"

func TestRequiresUpstreamStreamMode(t *testing.T) {
	for _, raw := range []string{
		`{"channel_settings":{"p":{"set":{"/preferences/upstream_stream_mode":"stream_only"}}}}`,
		`{"temporary_channels":[{"definition":{"preferences":{"upstream_stream_mode":{"models":{"m":"stream_only"}}}}}]}`,
		`{"set":{"/preferences/upstream_stream_mode/models/m":"stream_only"}}`,
	} {
		if !requiresUpstreamStreamMode([]byte(raw)) {
			t.Fatalf("lost capability: %s", raw)
		}
	}
	if requiresUpstreamStreamMode([]byte(`{"set":{"/preferences/cooldown_period":30}}`)) {
		t.Fatal("blocked legacy configuration")
	}
}
