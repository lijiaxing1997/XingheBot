package cluster

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	DefaultAuthSkew  = 60 * time.Second
	DefaultNonceTTL  = 3 * time.Minute
	DefaultNonceMax  = 20000
	DefaultHBTimeout = 15 * time.Second
)

func SignRegister(secret []byte, slaveID string, ts int64, nonce string) (string, error) {
	if len(secret) == 0 {
		return "", errors.New("secret is empty")
	}
	id := strings.TrimSpace(slaveID)
	if id == "" {
		return "", errors.New("slave_id is required")
	}
	n := strings.TrimSpace(nonce)
	if n == "" {
		return "", errors.New("nonce is required")
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(id))
	_, _ = mac.Write([]byte("\n"))
	_, _ = mac.Write([]byte(fmt.Sprintf("%d", ts)))
	_, _ = mac.Write([]byte("\n"))
	_, _ = mac.Write([]byte(n))
	sum := mac.Sum(nil)
	return hex.EncodeToString(sum), nil
}

type AuthVerifier struct {
	Secret    []byte
	Skew      time.Duration
	Nonces    *NonceCache
	Now       func() time.Time
	AllowZero bool
}

func (v AuthVerifier) VerifyRegister(slaveID string, auth RegisterAuth) error {
	secret := v.Secret
	if len(secret) == 0 {
		return errors.New("auth secret is not configured")
	}
	nowFn := v.Now
	if nowFn == nil {
		nowFn = func() time.Time { return time.Now().UTC() }
	}
	now := nowFn()

	if !v.AllowZero {
		if strings.TrimSpace(slaveID) == "" {
			return errors.New("slave_id is required")
		}
		if auth.TS == 0 || strings.TrimSpace(auth.Nonce) == "" || strings.TrimSpace(auth.Sig) == "" {
			return errors.New("auth fields ts/nonce/sig are required")
		}
	}

	skew := v.Skew
	if skew <= 0 {
		skew = DefaultAuthSkew
	}
	ts := time.Unix(auth.TS, 0).UTC()
	if ts.IsZero() {
		return errors.New("invalid auth.ts")
	}
	if ts.After(now.Add(skew)) || ts.Before(now.Add(-skew)) {
		return fmt.Errorf("auth.ts outside allowed skew (ts=%s now=%s skew=%s)", ts.Format(time.RFC3339), now.Format(time.RFC3339), skew)
	}

	if v.Nonces != nil {
		if ok := v.Nonces.Use(strings.TrimSpace(auth.Nonce), now); !ok {
			return errors.New("auth.nonce already used")
		}
	}

	expected, err := SignRegister(secret, slaveID, auth.TS, auth.Nonce)
	if err != nil {
		return err
	}
	got := strings.TrimSpace(auth.Sig)
	if got == "" {
		return errors.New("auth.sig is required")
	}
	// Constant-time compare on decoded bytes.
	expBytes, err := hex.DecodeString(expected)
	if err != nil {
		return err
	}
	gotBytes, err := hex.DecodeString(got)
	if err != nil {
		return err
	}
	if !hmac.Equal(expBytes, gotBytes) {
		return errors.New("invalid auth signature")
	}
	return nil
}

