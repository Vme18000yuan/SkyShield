package repository

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
)

// PostgreSQL text cannot contain NUL. Keep it visible in the activity display;
// the original tool output remains in the session transcript and artifacts.
func printableActivityText(value string) string {
	return strings.ReplaceAll(strings.ToValidUTF8(value, "\uFFFD"), "\x00", `\x00`)
}

const sideDocumentCodec = "json-string-v1"

// JSONB also rejects escaped NUL inside strings. A second JSON string layer
// stores the exact serialized document without altering reasoning, tool inputs
// or outputs. Ordinary documents retain the existing layout. Ordering metadata
// stays at the root for the snapshot compare-and-swap query.
func marshalSideDocument(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil || !bytes.Contains(raw, []byte(`\u0000`)) {
		return raw, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	payload, _ := json.Marshal(string(raw))
	codec, _ := json.Marshal(sideDocumentCodec)
	envelope := map[string]json.RawMessage{"_tianqiong_codec": codec, "_tianqiong_payload": payload}
	for _, key := range []string{"run_id", "version", "status"} {
		if field, ok := fields[key]; ok {
			envelope[key] = field
		}
	}
	return json.Marshal(envelope)
}

func unmarshalSideDocument(raw []byte, destination any) error {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return err
	}
	if marker, encoded := envelope["_tianqiong_codec"]; encoded {
		var codec, payload string
		if json.Unmarshal(marker, &codec) != nil || codec != sideDocumentCodec || json.Unmarshal(envelope["_tianqiong_payload"], &payload) != nil {
			return errors.New("invalid side-question document encoding")
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal([]byte(payload), &fields); err != nil {
			return err
		}
		// Cancel/expiry updates add status and error at the root. Preserve those
		// authoritative overrides when reading a losslessly encoded exchange.
		for _, key := range []string{"run_id", "version", "status", "error"} {
			if field, ok := envelope[key]; ok {
				fields[key] = field
			}
		}
		var err error
		raw, err = json.Marshal(fields)
		if err != nil {
			return err
		}
	}
	return json.Unmarshal(raw, destination)
}
