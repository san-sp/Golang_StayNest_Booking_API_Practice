package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

const maxJSONBodySize = 1 << 20 // 1 MiB

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBodySize)

	decoder := json.NewDecoder(r.Body)

	// Decode the first JSON value without losing its original structure.
	var raw json.RawMessage

	if err := decoder.Decode(&raw); err != nil {
		return err
	}

	// The top-level JSON value must be an object.
	trimmed := bytes.TrimSpace(raw)

	if len(trimmed) == 0 || trimmed[0] != '{' {
		return errors.New("request body must contain a JSON object")
	}

	// Decode into the destination while rejecting unknown fields.
	objectDecoder := json.NewDecoder(bytes.NewReader(raw))
	objectDecoder.DisallowUnknownFields()

	if err := objectDecoder.Decode(dst); err != nil {
		return err
	}

	// A request must contain exactly one JSON document.
	var extra any

	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("request body must contain exactly one JSON document")
		}

		return err
	}

	return nil
}
