package controllers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// The documented error shape is {success, message, errors: []}. Bind
// failures used to answer with a "details" string instead, so a client
// reading the contract found no errors array at all.

func bindFailure(t *testing.T, body string) (*gin.Context, *httptest.ResponseRecorder, error) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	var input struct {
		Company string `json:"company" binding:"required"`
		Round   string `json:"round" binding:"required"`
	}
	err := c.ShouldBindJSON(&input)
	if err == nil {
		t.Fatal("expected the bind to fail")
	}
	return c, recorder, err
}

func decodeBody(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON: %v (%s)", err, recorder.Body.String())
	}
	return body
}

func TestWriteBindError_UsesTheDocumentedErrorShape(t *testing.T) {
	c, recorder, err := bindFailure(t, `{"company":`)
	writeBindError(c, err)

	if recorder.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}

	body := decodeBody(t, recorder)
	if body["success"] != false {
		t.Errorf("success = %v, want false", body["success"])
	}
	if _, ok := body["message"].(string); !ok {
		t.Errorf("message is missing or not a string: %v", body["message"])
	}
	if _, present := body["details"]; present {
		t.Error("response still carries a details field, which is not part of the contract")
	}

	errs, ok := body["errors"].([]any)
	if !ok {
		t.Fatalf("errors = %v (%T), want an array", body["errors"], body["errors"])
	}
	if len(errs) == 0 {
		t.Fatal("errors array is empty")
	}
	for _, entry := range errs {
		if _, ok := entry.(string); !ok {
			t.Errorf("errors entry %v is %T, want a string", entry, entry)
		}
	}
}

// TestWriteBindError_ReportsEveryFailedField keeps the array useful:
// validator reports one line per field, and collapsing them into a
// single blob would tell the user less than the contract allows.
func TestWriteBindError_ReportsEveryFailedField(t *testing.T) {
	c, recorder, err := bindFailure(t, `{}`)
	writeBindError(c, err)

	body := decodeBody(t, recorder)
	errs, ok := body["errors"].([]any)
	if !ok {
		t.Fatalf("errors = %v, want an array", body["errors"])
	}
	if len(errs) != 2 {
		t.Errorf("got %d errors %v, want one per missing field (company, round)", len(errs), errs)
	}
}

func TestWriteBindError_NeverReturnsAnEmptyArray(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader("{}"))

	writeBindError(c, errString(""))

	body := decodeBody(t, recorder)
	errs, ok := body["errors"].([]any)
	if !ok || len(errs) == 0 {
		t.Fatalf("errors = %v, want a non-empty array even for a blank error", body["errors"])
	}
}

// errString is an error whose message is exactly the given text.
type errString string

func (e errString) Error() string { return string(e) }
