package controllers

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// The documented error envelope is
//
//	{ "success": false, "message": "...", "errors": ["...", "..."] }
//
// Bind failures used to answer with a "details" string instead, which is
// not part of the contract: a client that reads the documented shape
// found no errors array at all and had nothing to show the user. Every
// handler now goes through writeBindError so the shape is decided in one
// place.

// writeBindError answers a request whose body could not be bound, using
// the documented error envelope.
//
// The binding validator reports one line per failed field, so the lines
// become one array entry each rather than a single blob.
func writeBindError(c *gin.Context, err error) {
	c.JSON(http.StatusBadRequest, gin.H{
		"success": false,
		"message": "Invalid request body",
		"errors":  bindErrorMessages(err),
	})
}

// bindErrorMessages splits a binding error into one message per failed
// field. It never returns an empty slice — an error the client cannot
// see is worse than a generic one.
func bindErrorMessages(err error) []string {
	if err == nil {
		return []string{"Request body is invalid"}
	}

	messages := make([]string, 0, 2)
	for _, line := range strings.Split(err.Error(), "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			messages = append(messages, trimmed)
		}
	}
	if len(messages) == 0 {
		return []string{"Request body is invalid"}
	}
	return messages
}
