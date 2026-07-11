// Package apierr reproduces hakatime's error JSON envelope (Errors.hs).
// Every error response body is {"error": "<msg>"[, "message": "<extra>"]} with
// Content-Type application/json;charset=utf-8.
package apierr

import (
	"net/http"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/model"
	"github.com/labstack/echo/v5"
)

// Error is an HTTP error carrying the hakatime error envelope.
type Error struct {
	Status  int
	Message string
	Extra   *string
}

func (e *Error) Error() string { return e.Message }

// New builds an *Error.
func New(status int, msg string, extra *string) *Error {
	return &Error{Status: status, Message: msg, Extra: extra}
}

// Write emits the error envelope onto the Echo context.
func (e *Error) Write(c *echo.Context) error {
	c.Response().Header().Set(echo.HeaderContentType, "application/json;charset=utf-8")
	return c.JSON(e.Status, model.APIErrorData{Error: e.Message, Message: e.Extra})
}

// Predefined errors matching Haka.Errors.

func MissingAuth() *Error {
	return New(http.StatusBadRequest, "Missing the 'Authorization' header field", nil)
}

func MissingQueryParam(param string) *Error {
	return New(http.StatusBadRequest, "Missing query parameter "+param, nil)
}

func MissingRefreshTokenCookie() *Error {
	return New(http.StatusBadRequest, "Missing the 'refresh_token' cookie", nil)
}

func InvalidToken() *Error {
	return New(http.StatusForbidden, "The given api token doesn't belong to a user", nil)
}

func InvalidRelation(user, project string) *Error {
	return New(http.StatusNotFound,
		"The user "+user+" doesn't have access to project "+project, nil)
}

func ExpiredRefreshToken() *Error {
	return New(http.StatusForbidden, "The given api token has expired", nil)
}

func DisabledRegistration() *Error {
	return New(http.StatusForbidden, "Registration is disabled", nil)
}

func UsernameExists(u string) *Error {
	return New(http.StatusConflict, "The username "+u+" already exists", nil)
}

func RegisterError() *Error {
	return New(http.StatusConflict, "The registration failed due to an internal error", nil)
}

func InvalidCredentials() *Error {
	return New(http.StatusForbidden, "Invalid credentials", nil)
}

func MissingGithubToken() *Error {
	return New(http.StatusInternalServerError, "The environment variable GITHUB_TOKEN is not set", nil)
}

func BadRequest(msg string) *Error {
	return New(http.StatusBadRequest, msg, nil)
}

func NotFound(msg string) *Error {
	return New(http.StatusNotFound, msg, nil)
}

func Generic() *Error {
	return New(http.StatusInternalServerError, "An internal error occurred", nil)
}

func GenericHTTP(msg string, extra *string) *Error {
	return New(http.StatusInternalServerError, msg, extra)
}
