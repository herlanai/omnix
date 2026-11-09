// Upload admission matches attachment/admission: base64 must round-trip to the
// identical encoding so alternate representations cannot bypass validation.
package attachment

import (
	"encoding/base64"
	"errors"
)

// ErrInvalidBase64 indicates an empty or noncanonical base64 payload.
var ErrInvalidBase64 = errors.New("attachment: image upload is not canonical base64")

// DecodeCanonicalBase64 decodes canonical base64 or returns ErrInvalidBase64.
func DecodeCanonicalBase64(data string) ([]byte, error) {
	if data == "" {
		return nil, ErrInvalidBase64
	}
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return nil, ErrInvalidBase64
	}
	if base64.StdEncoding.EncodeToString(raw) != data {
		return nil, ErrInvalidBase64
	}
	return raw, nil
}
