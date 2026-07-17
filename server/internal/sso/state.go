package sso

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

// CookieName is the OIDC state cookie name.
const CookieName = "multica_sso_state"

// CookieTTL is the max lifetime of the state cookie (5 minutes).
const CookieTTL = 5 * time.Minute

// StatePayload is the data encoded (HMAC-SHA256 signed) in the state cookie.
type StatePayload struct {
	State        string `json:"state"`
	CodeVerifier string `json:"code_verifier"`
	Next         string `json:"next,omitempty"` // sanitized relative path
	Exp          int64  `json:"exp"`            // unix timestamp
}

// SetStateCookie sets the signed, HttpOnly, SameSite=Lax state cookie.
// signingKey is typically auth.JWTSecret().
func SetStateCookie(w http.ResponseWriter, payload StatePayload, signingKey []byte) error {
	payload.Exp = time.Now().Add(CookieTTL).Unix()

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	encoded := base64.RawURLEncoding.EncodeToString(body)
	sig := hmacSHA256(encoded, signingKey)

	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    encoded + "." + sig,
		Path:     "/",
		MaxAge:   int(CookieTTL.Seconds()),
		HttpOnly: true,
		Secure:   false, // dev-friendly; production should set Secure via FRONTEND_ORIGIN
		SameSite: http.SameSiteLaxMode,
	})
	return nil
}

// ReadStateCookie reads + verifies the state cookie, returning the payload.
// Returns error if cookie is missing, expired, or signature is invalid.
func ReadStateCookie(r *http.Request, signingKey []byte) (*StatePayload, error) {
	c, err := r.Cookie(CookieName)
	if err != nil {
		return nil, errors.New("sso: state cookie missing")
	}

	parts := strings.SplitN(c.Value, ".", 2)
	if len(parts) != 2 {
		return nil, errors.New("sso: state cookie malformed")
	}
	encoded, gotSig := parts[0], parts[1]

	wantSig := hmacSHA256(encoded, signingKey)
	if !hmac.Equal([]byte(gotSig), []byte(wantSig)) {
		return nil, errors.New("sso: state cookie signature invalid")
	}

	body, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, errors.New("sso: state cookie decode failed")
	}

	var payload StatePayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, errors.New("sso: state cookie parse failed")
	}

	if time.Now().Unix() > payload.Exp {
		return nil, errors.New("sso: state cookie expired")
	}
	return &payload, nil
}

// ClearStateCookie deletes the state cookie.
func ClearStateCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func hmacSHA256(message string, key []byte) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(message))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}