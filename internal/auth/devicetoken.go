package auth

import (
	"encoding/json"
	"strings"
	"time"
)

// Device tokens.
//
// A session pod runs the agent, which executes model-chosen code. Handing it the
// user's session token would mean a prompt-injected model could lift that token
// and act as the user against the whole API. So a pod gets a credential that can
// do exactly one thing: attach as one named device, on behalf of one identity,
// until it expires.
//
// Deliberately not accepted by identify(): this is not a login. It only opens
// the relay, and every session the device serves is still owned by the identity
// named inside it.

type deviceClaims struct {
	Email  string `json:"e"`
	Device string `json:"d"`
	Exp    int64  `json:"x"`
}

// devicePrefix marks the credential so it can never be confused with a session
// token, in either direction.
const devicePrefix = "dv1."

// MintDeviceToken issues a credential that authorises attaching as device, on
// behalf of email, for ttl.
func (a *Authenticator) MintDeviceToken(email, device string, ttl time.Duration) (string, error) {
	b, err := json.Marshal(deviceClaims{
		Email: email, Device: device, Exp: time.Now().Add(ttl).Unix(),
	})
	if err != nil {
		return "", err
	}
	return devicePrefix + a.sign(string(b)), nil
}

// VerifyDeviceToken checks a device credential and returns who it acts for and
// which device it may attach as.
func (a *Authenticator) VerifyDeviceToken(tok string) (email, device string, ok bool) {
	body, found := strings.CutPrefix(tok, devicePrefix)
	if !found {
		return "", "", false
	}
	payload, valid := a.verifyCookie(body)
	if !valid {
		return "", "", false
	}
	var c deviceClaims
	if err := json.Unmarshal([]byte(payload), &c); err != nil {
		return "", "", false
	}
	if c.Email == "" || c.Device == "" || time.Now().Unix() > c.Exp {
		return "", "", false
	}
	return c.Email, c.Device, true
}

// IsDeviceToken reports whether a bearer value is a device credential, so
// callers can reject it where a full identity is required.
func IsDeviceToken(tok string) bool { return strings.HasPrefix(tok, devicePrefix) }
