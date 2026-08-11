package main

import (
	"encoding/json"
	"io"
	"net/http"
)

// jmap is a loosely-typed JSON object, mirroring how the original
// server.js treats request/response bodies as plain JS objects.
type jmap = map[string]any

func jsonResponse(w http.ResponseWriter, status int, body any) {
	payload, err := json.Marshal(body)
	if err != nil {
		payload = []byte(`{"error":"Internal server error"}`)
		status = http.StatusInternalServerError
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(payload)
}

// readBody reads and JSON-decodes a request body into a jmap. An empty
// body decodes to an empty map, matching server.js's readBody().
func readBody(r *http.Request) (jmap, error) {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return jmap{}, nil
	}
	var body jmap
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	return body, nil
}

func mergeMaps(base jmap, overlay jmap) jmap {
	out := make(jmap, len(base)+len(overlay))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range overlay {
		out[k] = v
	}
	return out
}

func strField(m jmap, key string) (string, bool) {
	v, ok := m[key]
	if !ok || v == nil {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

func boolField(m jmap, key string) bool {
	v, ok := m[key]
	if !ok {
		return false
	}
	b, _ := v.(bool)
	return b
}
