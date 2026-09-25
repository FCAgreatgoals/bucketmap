package engine

import (
	"encoding/json"
	"regexp"
	"strings"
)

// Recorded bodies never carry a credential: webhook and interaction tokens
// are replaced, as field values and inside URLs.

var webhookURL = regexp.MustCompile(`(/webhooks/\d+/)[A-Za-z0-9_.\-]+`)

// tokenFields are the fields whose value is a credential.
var tokenFields = map[string]bool{"token": true, "webhook_token": true, "interaction_token": true}

const hidden = "<hidden>"

// recordRequest keeps what the engine sent: the JSON body, or for multipart
// its JSON part and the names of its files.
func recordRequest(body any) json.RawMessage {
	switch b := body.(type) {
	case nil:
		return nil
	case multipartBody:
		files := make([]string, len(b.files))
		for i, f := range b.files {
			files[i] = f.name
		}
		return marshal(map[string]any{"multipart": true, "payload": b.payload, "fields": b.fields, "files": files})
	default:
		return marshal(scrub(roundTrip(b)))
	}
}

// recordResponse keeps a JSON answer, tokens hidden, and notes the type of any
// other answer rather than its bytes.
func recordResponse(contentType string, payload []byte) json.RawMessage {
	if len(payload) == 0 {
		return nil
	}
	var v any
	if strings.HasPrefix(contentType, "application/json") && json.Unmarshal(payload, &v) == nil {
		return marshal(scrub(v))
	}
	return marshal(map[string]any{"content_type": contentType, "size": len(payload)})
}

// scrub hides tokens in a decoded JSON value, in place.
func scrub(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if tokenFields[k] {
				if _, isString := val.(string); isString {
					t[k] = hidden
					continue
				}
			}
			t[k] = scrub(val)
		}
		return t
	case []any:
		for i := range t {
			t[i] = scrub(t[i])
		}
		return t
	case string:
		return webhookURL.ReplaceAllString(t, "${1}"+hidden)
	default:
		return v
	}
}

func roundTrip(v any) any {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var out any
	_ = json.Unmarshal(raw, &out)
	return out
}

func marshal(v any) json.RawMessage {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return raw
}
