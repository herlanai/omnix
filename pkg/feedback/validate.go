// Validation matches message-feedback upstream: only thumbs up/down are stored
// as valid ratings (zero means unrated). A supplied note must contain non-whitespace.
package feedback

import (
	"errors"
	"strings"
)

// ErrInvalidRating indicates a rating other than thumbs up or down.
var ErrInvalidRating = errors.New("feedback: rating must be thumbs up or down")

// ErrEmptyNote indicates a supplied note containing only whitespace.
var ErrEmptyNote = errors.New("feedback: note must contain a non-whitespace character")

// ValidateRating accepts only ThumbsUp and ThumbsDown.
func ValidateRating(r Rating) error {
	if r != RatingThumbsUp && r != RatingThumbsDown {
		return ErrInvalidRating
	}
	return nil
}

// ValidateNote requires non-whitespace for nonempty notes; empty means omitted.
func ValidateNote(note string) error {
	if note == "" {
		return nil
	}
	if strings.TrimSpace(note) == "" {
		return ErrEmptyNote
	}
	return nil
}
