package helper

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

func buildReq(t *testing.T, body string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "http://example.com/v1/messages", bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	return req
}

func readBody(t *testing.T, req *http.Request) map[string]any {
	t.Helper()
	b, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	return m
}

// 1. JSON null deletes an existing field.
func TestParamOverride_NullDeletesField(t *testing.T) {
	req := buildReq(t, `{"model":"x","prompt_cache_key":"abc","prompt_cache_retention":"24h"}`)
	override := `{"prompt_cache_key":null,"prompt_cache_retention":null}`
	if err := ApplyParamOverride(req, &override); err != nil {
		t.Fatalf("apply: %v", err)
	}
	m := readBody(t, req)
	if _, ok := m["prompt_cache_key"]; ok {
		t.Errorf("prompt_cache_key should be deleted, got %v", m["prompt_cache_key"])
	}
	if _, ok := m["prompt_cache_retention"]; ok {
		t.Errorf("prompt_cache_retention should be deleted, got %v", m["prompt_cache_retention"])
	}
	if m["model"] != "x" {
		t.Errorf("model should be preserved, got %v", m["model"])
	}
}

// 2. Null on a missing key is a harmless no-op.
func TestParamOverride_NullMissingKeyNoop(t *testing.T) {
	req := buildReq(t, `{"model":"x"}`)
	override := `{"prompt_cache_key":null}`
	if err := ApplyParamOverride(req, &override); err != nil {
		t.Fatalf("apply: %v", err)
	}
	m := readBody(t, req)
	if len(m) != 1 || m["model"] != "x" {
		t.Errorf("body should be unchanged, got %v", m)
	}
}

// 3. Non-null values still set/overwrite (original behaviour preserved).
func TestParamOverride_SetStillWorks(t *testing.T) {
	req := buildReq(t, `{"model":"x","temperature":1}`)
	override := `{"temperature":0.5,"top_p":0.9}`
	if err := ApplyParamOverride(req, &override); err != nil {
		t.Fatalf("apply: %v", err)
	}
	m := readBody(t, req)
	if m["temperature"] != 0.5 {
		t.Errorf("temperature should be overwritten to 0.5, got %v", m["temperature"])
	}
	if m["top_p"] != 0.9 {
		t.Errorf("top_p should be set to 0.9, got %v", m["top_p"])
	}
	if m["model"] != "x" {
		t.Errorf("model should be preserved, got %v", m["model"])
	}
}

// 4. Mixed set + delete in one override.
func TestParamOverride_MixedSetAndDelete(t *testing.T) {
	req := buildReq(t, `{"model":"x","prompt_cache_key":"abc","stream":false}`)
	override := `{"prompt_cache_key":null,"stream":true}`
	if err := ApplyParamOverride(req, &override); err != nil {
		t.Fatalf("apply: %v", err)
	}
	m := readBody(t, req)
	if _, ok := m["prompt_cache_key"]; ok {
		t.Errorf("prompt_cache_key should be deleted, got %v", m["prompt_cache_key"])
	}
	if m["stream"] != true {
		t.Errorf("stream should be true, got %v", m["stream"])
	}
	if m["model"] != "x" {
		t.Errorf("model should be preserved, got %v", m["model"])
	}
}

// 5. Empty override leaves the body untouched.
func TestParamOverride_EmptyOverride(t *testing.T) {
	req := buildReq(t, `{"model":"x","prompt_cache_key":"abc"}`)
	empty := ""
	if err := ApplyParamOverride(req, &empty); err != nil {
		t.Fatalf("apply: %v", err)
	}
	m := readBody(t, req)
	if m["prompt_cache_key"] != "abc" {
		t.Errorf("body should be untouched with empty override, got %v", m)
	}
}
