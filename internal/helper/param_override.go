package helper

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ApplyParamOverride merges a JSON-object override into an outbound JSON request body.
// Empty overrides, nil bodies, and non-object request bodies are ignored.
//
// A JSON null value for a key deletes that key from the request body, allowing
// callers to strip unsupported fields (e.g. prompt_cache_key) before forwarding
// the request upstream. Any other value sets the key as before.
func ApplyParamOverride(request *http.Request, paramOverride *string) error {
	if request == nil || request.Body == nil || paramOverride == nil || strings.TrimSpace(*paramOverride) == "" {
		return nil
	}

	body, err := io.ReadAll(request.Body)
	if err != nil {
		return fmt.Errorf("failed to read request body: %w", err)
	}

	restoreBody := func() {
		request.Body = io.NopCloser(bytes.NewReader(body))
		request.ContentLength = int64(len(body))
		request.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(body)), nil
		}
	}

	var bodyMap map[string]any
	if err := json.Unmarshal(body, &bodyMap); err != nil {
		restoreBody()
		return nil
	}

	var override map[string]any
	if err := json.Unmarshal([]byte(*paramOverride), &override); err != nil {
		restoreBody()
		return nil
	}

	for key, value := range override {
		if value == nil {
			// JSON null means "delete this field" instead of setting it.
			delete(bodyMap, key)
			continue
		}
		bodyMap[key] = value
	}

	modifiedBody, err := json.Marshal(bodyMap)
	if err != nil {
		return fmt.Errorf("failed to marshal request body with param override: %w", err)
	}

	request.Body = io.NopCloser(bytes.NewReader(modifiedBody))
	request.ContentLength = int64(len(modifiedBody))
	request.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(modifiedBody)), nil
	}
	return nil
}
