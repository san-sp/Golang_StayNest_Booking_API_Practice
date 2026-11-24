package handler

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type testJSONRequest struct {
	Name string `json:"name"`
	Age  int    `json:"age"`
}

func TestDecodeJSON(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{
			name: "valid JSON",
			body: `{"name":"John","age":30}`,
		},
		{
			name: "valid JSON with whitespace",
			body: `  {"name":"John","age":30}  `,
		},
		{
			name:    "malformed JSON",
			body:    `{"name":`,
			wantErr: true,
		},
		{
			name:    "unknown field",
			body:    `{"name":"John","age":30,"admin":true}`,
			wantErr: true,
		},
		{
			name:    "multiple JSON documents",
			body:    `{"name":"John","age":30} {"name":"Jane","age":25}`,
			wantErr: true,
		},
		{
			name:    "trailing invalid data",
			body:    `{"name":"John","age":30} invalid`,
			wantErr: true,
		},
		{
			name:    "empty body",
			body:    "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(
				http.MethodPost,
				"/test",
				strings.NewReader(tt.body),
			)

			rec := httptest.NewRecorder()

			var input testJSONRequest

			err := decodeJSON(rec, req, &input)

			if (err != nil) != tt.wantErr {
				t.Fatalf("decodeJSON() error = %v, wantErr %v",
					err, tt.wantErr)
			}

			if err == nil {
				if input.Name != "John" || input.Age != 30 {
					t.Errorf("unexpected decoded request: %+v", input)
				}
			}
		})
	}
}

func TestDecodeJSONRejectsOversizedBody(t *testing.T) {
	body := `{"name":"` + strings.Repeat(
		"a",
		maxJSONBodySize,
	) + `"}`

	req := httptest.NewRequest(
		http.MethodPost,
		"/test",
		strings.NewReader(body),
	)

	rec := httptest.NewRecorder()

	var input testJSONRequest

	err := decodeJSON(rec, req, &input)
	if err == nil {
		t.Fatal("expected oversized request body to be rejected")
	}

	var maxBytesErr *http.MaxBytesError

	if !errors.As(err, &maxBytesErr) {
		t.Fatalf("expected MaxBytesError, got %T: %v", err, err)
	}
}

func TestDecodeJSONRejectsNonObjectTopLevel(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "null",
			body: `null`,
		},
		{
			name: "array",
			body: `[]`,
		},
		{
			name: "string",
			body: `"hello"`,
		},
		{
			name: "number",
			body: `123`,
		},
		{
			name: "boolean",
			body: `true`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(
				http.MethodPost,
				"/test",
				strings.NewReader(tt.body),
			)

			rec := httptest.NewRecorder()

			var input testJSONRequest

			err := decodeJSON(rec, req, &input)

			if err == nil {
				t.Fatal("expected non-object JSON to be rejected")
			}
		})
	}
}

func TestDecodeJSONAcceptsEmptyObject(t *testing.T) {
	req := httptest.NewRequest(
		http.MethodPost,
		"/test",
		strings.NewReader(`{}`),
	)

	rec := httptest.NewRecorder()

	var input testJSONRequest

	if err := decodeJSON(rec, req, &input); err != nil {
		t.Fatalf("expected empty object to decode, got %v", err)
	}

	if input != (testJSONRequest{}) {
		t.Fatalf("expected zero-value struct, got %+v", input)
	}
}
