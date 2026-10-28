package oauth

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	ClientID = "hearth-web"
	Issuer   = "hearth"
)

var (
	ErrToken = errors.New("token")
)

type Claims struct {
	Sub       string `json:"sub"`
	Iss       string `json:"iss"`
	Aud       string `json:"aud"`
	Exp       int64  `json:"exp"`
	Iat       int64  `json:"iat"`
	JTI       string `json:"jti"`
	Household string `json:"household,omitempty"`
}

type Signer struct {
	kid  string
	priv ed25519.PrivateKey
	pub  ed25519.PublicKey
}

func LoadSigner(dir string) (*Signer, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "oauth-ed25519.key")
	buf, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, err
		}
		kidRaw := make([]byte, 8)
		if _, err := rand.Read(kidRaw); err != nil {
			return nil, err
		}
		kid := base64.RawURLEncoding.EncodeToString(kidRaw)
		body := kid + "\n" + base64.StdEncoding.EncodeToString(priv)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			return nil, err
		}
		return &Signer{kid: kid, priv: priv, pub: pub}, nil
	}
	parts := strings.SplitN(strings.TrimSpace(string(buf)), "\n", 2)
	if len(parts) != 2 || parts[0] == "" {
		return nil, errors.New("oauth signing key is unreadable")
	}
	priv, err := base64.StdEncoding.DecodeString(strings.TrimSpace(parts[1]))
	if err != nil {
		return nil, err
	}
	if len(priv) != ed25519.PrivateKeySize {
		return nil, errors.New("oauth signing key is unreadable")
	}
	key := ed25519.PrivateKey(priv)
	return &Signer{kid: parts[0], priv: key, pub: key.Public().(ed25519.PublicKey)}, nil
}

func LoadClientSecret(dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "oauth-client.txt")
	buf, err := os.ReadFile(path)
	if err == nil {
		secret := strings.TrimSpace(string(buf))
		if secret == "" {
			return "", errors.New("oauth client secret is empty")
		}
		return secret, nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	secret := base64.RawURLEncoding.EncodeToString(raw)
	if err := os.WriteFile(path, []byte(secret+"\n"), 0o600); err != nil {
		return "", err
	}
	return secret, nil
}

func (s *Signer) Sign(c Claims) (string, error) {
	header, err := json.Marshal(map[string]string{"alg": "EdDSA", "typ": "JWT", "kid": s.kid})
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	signing := b64(header) + "." + b64(payload)
	sig := ed25519.Sign(s.priv, []byte(signing))
	return signing + "." + b64(sig), nil
}

func (s *Signer) Verify(raw string, now time.Time) (Claims, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return Claims{}, ErrToken
	}
	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return Claims{}, ErrToken
	}
	var header struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := json.Unmarshal(headerJSON, &header); err != nil || header.Alg != "EdDSA" || header.Kid != s.kid {
		return Claims{}, ErrToken
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || !ed25519.Verify(s.pub, []byte(parts[0]+"."+parts[1]), sig) {
		return Claims{}, ErrToken
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Claims{}, ErrToken
	}
	var claims Claims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return Claims{}, ErrToken
	}
	if claims.Iss != Issuer || claims.Aud != ClientID || claims.Sub == "" {
		return Claims{}, ErrToken
	}
	if claims.Exp <= now.Unix() {
		return Claims{}, ErrToken
	}
	return claims, nil
}

func (s *Signer) JWKS() []byte {
	keys := map[string]any{
		"keys": []map[string]string{{
			"kty": "OKP",
			"crv": "Ed25519",
			"kid": s.kid,
			"use": "sig",
			"alg": "EdDSA",
			"x":   base64.RawURLEncoding.EncodeToString(s.pub),
		}},
	}
	buf, _ := json.Marshal(keys)
	return buf
}

func b64(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}
