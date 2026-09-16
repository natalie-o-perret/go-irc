// Package sasl provides SASL authentication mechanisms for IRC.
package sasl

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"fmt"
	"hash"
	"strconv"
	"strings"

	"golang.org/x/crypto/pbkdf2"
)

// Mechanism is an abstract SASL authentication mechanism.
type Mechanism interface {
	// Name returns the IANA SASL mechanism name (e.g. "PLAIN", "SCRAM-SHA-256").
	Name() string
	// Next processes a server challenge and returns the next client message.
	// An empty challenge is passed for the first call.
	// Returns done=true when authentication is complete (nothing more to send).
	Next(challenge []byte) (response []byte, done bool, err error)
}

// ---------------------------------------------------------------------------
// PLAIN
// ---------------------------------------------------------------------------

// Plain implements SASL PLAIN (RFC 4616).
type Plain struct {
	Username string
	Password string
	// AuthzID is the optional authorization identity; usually empty.
	AuthzID string
}

// Name returns "PLAIN".
func (p *Plain) Name() string { return "PLAIN" }

// Next returns the PLAIN response: \x00user\x00pass.
func (p *Plain) Next(_ []byte) ([]byte, bool, error) {
	resp := p.AuthzID + "\x00" + p.Username + "\x00" + p.Password
	return []byte(resp), true, nil
}

// ---------------------------------------------------------------------------
// EXTERNAL
// ---------------------------------------------------------------------------

// External implements SASL EXTERNAL (RFC 4422); used with TLS client certs.
type External struct {
	AuthzID string
}

// Name returns "EXTERNAL".
func (e *External) Name() string { return "EXTERNAL" }

// Next returns the authorization identity (may be empty).
func (e *External) Next(_ []byte) ([]byte, bool, error) {
	return []byte(e.AuthzID), true, nil
}

// ---------------------------------------------------------------------------
// SCRAM-SHA-256 / SCRAM-SHA-512
// ---------------------------------------------------------------------------

type scramState int

const (
	scramStateInit scramState = iota
	scramStateServerFirst
	scramStateDone
)

// SCRAM implements SCRAM-SHA-256 or SCRAM-SHA-512 (RFC 5802).
type SCRAM struct {
	username    string
	password    string
	hashNew     func() hash.Hash
	hashName    string
	keyLen      int
	state       scramState
	clientNonce string
	clientFirst string
	serverSig   []byte
}

// NewScramSHA256 creates a SCRAM-SHA-256 mechanism.
func NewScramSHA256(username, password string) *SCRAM {
	return &SCRAM{
		username: username,
		password: password,
		hashNew:  sha256.New,
		hashName: "SCRAM-SHA-256",
		keyLen:   32,
	}
}

// NewScramSHA512 creates a SCRAM-SHA-512 mechanism.
func NewScramSHA512(username, password string) *SCRAM {
	return &SCRAM{
		username: username,
		password: password,
		hashNew:  sha512.New,
		hashName: "SCRAM-SHA-512",
		keyLen:   64,
	}
}

// Name returns the mechanism name.
func (s *SCRAM) Name() string { return s.hashName }

// Reset allows a SCRAM mechanism to be reused after reconnecting.
func (s *SCRAM) Reset() {
	s.state = scramStateInit
	s.clientNonce = ""
	s.clientFirst = ""
	s.serverSig = nil
}

// Next drives the SCRAM state machine.
func (s *SCRAM) Next(challenge []byte) ([]byte, bool, error) {
	switch s.state {
	case scramStateInit:
		// Generate client nonce
		nonceBuf := make([]byte, 18)
		if _, err := rand.Read(nonceBuf); err != nil {
			return nil, false, err
		}
		s.clientNonce = base64.StdEncoding.EncodeToString(nonceBuf)
		// n,,n=user,r=nonce
		username := strings.NewReplacer("=", "=3D", ",", "=2C").Replace(s.username)
		s.clientFirst = "n=" + username + ",r=" + s.clientNonce
		msg := "n,," + s.clientFirst
		s.state = scramStateServerFirst
		return []byte(msg), false, nil

	case scramStateServerFirst:
		// Parse server-first-message
		sfm := string(challenge)
		parts := parseCSV(sfm)
		var serverNonce, saltB64, iterStr string
		for _, p := range parts {
			switch {
			case strings.HasPrefix(p, "r="):
				serverNonce = p[2:]
			case strings.HasPrefix(p, "s="):
				saltB64 = p[2:]
			case strings.HasPrefix(p, "i="):
				iterStr = p[2:]
			}
		}
		if serverNonce == "" || saltB64 == "" || iterStr == "" {
			return nil, false, fmt.Errorf("sasl: malformed server-first-message: %q", sfm)
		}
		if !strings.HasPrefix(serverNonce, s.clientNonce) {
			return nil, false, fmt.Errorf("sasl: server nonce does not start with client nonce")
		}

		salt, err := base64.StdEncoding.DecodeString(saltB64)
		if err != nil {
			return nil, false, fmt.Errorf("sasl: invalid salt: %w", err)
		}
		iterations, err := strconv.Atoi(iterStr)
		if err != nil || iterations <= 0 {
			return nil, false, fmt.Errorf("sasl: invalid iteration count: %q", iterStr)
		}

		// Derive keys
		saltedPassword := pbkdf2.Key([]byte(s.password), salt, iterations, s.keyLen, s.hashNew)

		clientKey := s.hmac(saltedPassword, []byte("Client Key"))
		storedKey := s.h(clientKey)

		// client-final-message-without-proof
		channelBinding := base64.StdEncoding.EncodeToString([]byte("n,,"))
		cfmwoProof := "c=" + channelBinding + ",r=" + serverNonce

		// AuthMessage
		authMsg := s.clientFirst + "," + sfm + "," + cfmwoProof

		clientSignature := s.hmac(storedKey, []byte(authMsg))
		clientProof := xorBytes(clientKey, clientSignature)

		// server-key / server-signature
		serverKey := s.hmac(saltedPassword, []byte("Server Key"))
		s.serverSig = s.hmac(serverKey, []byte(authMsg))

		proof := base64.StdEncoding.EncodeToString(clientProof)
		msg := cfmwoProof + ",p=" + proof
		s.state = scramStateDone
		return []byte(msg), false, nil

	case scramStateDone:
		// Verify server signature
		sv := string(challenge)
		if !strings.HasPrefix(sv, "v=") {
			return nil, false, fmt.Errorf("sasl: unexpected server-final: %q", sv)
		}
		sig, err := base64.StdEncoding.DecodeString(sv[2:])
		if err != nil {
			return nil, false, fmt.Errorf("sasl: invalid server signature: %w", err)
		}
		if !hmac.Equal(sig, s.serverSig) {
			return nil, false, fmt.Errorf("sasl: server signature mismatch")
		}
		return nil, true, nil
	}
	return nil, false, fmt.Errorf("sasl: unexpected state")
}

func (s *SCRAM) hmac(key, msg []byte) []byte {
	mac := hmac.New(s.hashNew, key)
	mac.Write(msg)
	return mac.Sum(nil)
}

func (s *SCRAM) h(data []byte) []byte {
	h := s.hashNew()
	h.Write(data)
	return h.Sum(nil)
}

func xorBytes(a, b []byte) []byte {
	out := make([]byte, len(a))
	for i := range a {
		out[i] = a[i] ^ b[i]
	}
	return out
}

func parseCSV(s string) []string {
	return strings.Split(s, ",")
}
