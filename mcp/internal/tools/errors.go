package tools

import (
	"errors"
	"fmt"

	"github.com/MuhammadAmas/QuickNotes/mcp/internal/auth"
	"github.com/MuhammadAmas/QuickNotes/mcp/internal/cloud"
	"github.com/MuhammadAmas/QuickNotes/mcp/internal/firestore"
)

// friendly turns a service error into a message for the AI model: what went wrong and what to do.
// Returning it from a handler makes the SDK send a tool result with isError=true. Messages are
// built from our own text and Firestore's error message, never from tokens or request URLs.
func friendly(err error, doing string) error {
	switch {
	case errors.Is(err, auth.ErrNotSignedIn):
		return err // already says how to sign in
	case errors.Is(err, cloud.ErrNotFound):
		return err
	case firestore.IsPermissionDenied(err):
		return fmt.Errorf("could not %s: QuickNotes' security rules refused it (only your own notes and notes shared with you can be used)", doing)
	}
	var apiErr *firestore.APIError
	if errors.As(err, &apiErr) {
		return fmt.Errorf("could not %s: %v", doing, apiErr)
	}
	return fmt.Errorf("could not %s: %v", doing, err)
}
