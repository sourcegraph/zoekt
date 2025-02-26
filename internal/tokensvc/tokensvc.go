package tokensvc

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"aidanwoods.dev/go-paseto"
	"github.com/cockroachdb/errors"
)

const (
	// PASETO_SIGNING_KEY is the environment variable that provides the seed for
	// generating PASETO signing keys. When set, signature validation is enforced.
	// When not set, the service runs in development mode with signature validation disabled.
	PASETO_SIGNING_KEY = "PASETO_SIGNING_SECRET"
	DefaultExpiration  = time.Second * 1
)

var (
	SignatureValidationEnforced atomic.Bool
	b64                         = base64.RawURLEncoding.Strict()
)

var tokenSeed = sync.OnceValue(func() string {
	if secret := os.Getenv(PASETO_SIGNING_KEY); secret != "" {
		SignatureValidationEnforced.Store(true)
		return secret
	}

	return strings.Repeat("F", 64)
})

type TokenValidator interface {
	ValidateAndParseToken(token string) (*paseto.Token, error)
}

type TokenGenerator interface {
	SignData(subject string, tokenData any, key string) (string, error)
}

type DefaultTokenGenerator struct {
	privateKey paseto.V4AsymmetricSecretKey
}

type DefaultTokenValidator struct {
	publicKey paseto.V4AsymmetricPublicKey
	parser    paseto.Parser
}

type contextKey struct{}

var subjectKey = contextKey{}

// WithSubject returns a new context with the given subject stored in it.
// This is used to propagate token subjects through context chains.
func WithSubject(ctx context.Context, subject string) context.Context {
	return context.WithValue(ctx, subjectKey, subject)
}

// SubjectFromContext extracts the subject from the context.
// Returns the subject string and a boolean indicating if a subject was present.
func SubjectFromContext(ctx context.Context) (string, bool) {
	subject, ok := ctx.Value(subjectKey).(string)
	return subject, ok
}

// NewTokenValidator creates a new TokenValidator that validates PASETO tokens
// against the given subject. It initializes with the global token seed and
// configures standard validation rules including expiration checking.
func NewTokenValidator(subject string) TokenValidator {
	secret, err := paseto.NewV4AsymmetricSecretKeyFromSeed(tokenSeed())
	if err != nil {
		panic(err)
	}

	// Standard rules, validate the expiry and the subject
	parser := paseto.NewParser()
	parser.AddRule(paseto.NotExpired())
	parser.AddRule(paseto.Subject(subject))

	return &DefaultTokenValidator{
		publicKey: secret.Public(),
		parser:    parser,
	}
}

// NewTokenGenerator creates a new TokenGenerator using the global token seed
// to initialize the PASETO private key for signing tokens.
func NewTokenGenerator() TokenGenerator {
	privateKey, err := paseto.NewV4AsymmetricSecretKeyFromSeed(tokenSeed())
	if err != nil {
		panic(err)
	}

	return &DefaultTokenGenerator{
		privateKey: privateKey,
	}
}

// SignData creates a new PASETO token with the given subject and data.
// The token includes standard claims (iat, exp) and the provided custom data
// under the specified key. The token is signed with the generator's private key.
func (g *DefaultTokenGenerator) SignData(subject string, tokenData any, key string) (string, error) {
	now := time.Now()
	exp := now.Add(DefaultExpiration)

	token := paseto.NewToken()
	if err := token.Set(key, tokenData); err != nil {
		return "", err
	}

	token.SetIssuedAt(now)
	token.SetExpiration(exp)
	token.SetSubject(subject)

	signed := token.V4Sign(g.privateKey, nil)

	return signed, nil
}

// ValidateAndParseToken validates and parses a PASETO token string.
// It performs cryptographic validation and checks standard rules (expiration, subject).
// If signature validation is not enforced, it falls back to
// unsafe parsing.
func (v *DefaultTokenValidator) ValidateAndParseToken(tokenString string) (*paseto.Token, error) {
	// this will fail if parsing failes, cryptographic checks fail, or validation rules fail
	token, err := v.parser.ParseV4Public(v.publicKey, tokenString, nil)
	if err != nil {
		if !SignatureValidationEnforced.Load() {
			return v.unsafeParseToken(tokenString)
		}
		return nil, err
	}

	return token, nil
}

// unsafeParseToken parses a PASETO token WITHOUT cryptographic validation.
// This method is used as a fallback when signature validation is not enforced.
func (v *DefaultTokenValidator) unsafeParseToken(tokenString string) (*paseto.Token, error) {
	parts := strings.Split(tokenString, ".")
	if len(parts) != 3 {
		return nil, errors.Newf("invalid token format: expected 3 parts, got %d", len(parts))
	}

	encoded := parts[2]
	bts, err := b64.DecodeString(encoded)
	if err != nil {
		return nil, errors.Wrap(err, "failed to decode base64")
	}

	if len(bts) <= 64 {
		return nil, errors.Wrap(err, "token payload")
	}

	// Parse the raw JSON payload first (excluding the signature)
	var rawPayload map[string]interface{}
	if err := json.Unmarshal(bts[0:len(bts)-64], &rawPayload); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal token data")
	}

	token := paseto.NewToken()

	// Set the standard claims
	if exp, ok := rawPayload["exp"].(string); ok {
		expTime, err := time.Parse(time.RFC3339, exp)
		if err != nil {
			return nil, errors.Wrap(err, "invalid expiration time")
		}
		token.SetExpiration(expTime)
	}
	if iat, ok := rawPayload["iat"].(string); ok {
		iatTime, err := time.Parse(time.RFC3339, iat)
		if err != nil {
			return nil, errors.Wrap(err, "invalid issued at time")
		}
		token.SetIssuedAt(iatTime)
	}
	if sub, ok := rawPayload["sub"].(string); ok {
		token.SetSubject(sub)
	}

	// Set the actor claim
	if actor, ok := rawPayload["actor"]; ok {
		if err := token.Set("actor", actor); err != nil {
			return nil, errors.Wrap(err, "failed to set actor claim")
		}
	}

	// Set the tenant claim
	if tenant, ok := rawPayload["tenant"]; ok {
		if err := token.Set("tenant", tenant); err != nil {
			return nil, errors.Wrap(err, "failed to set tenant claim")
		}
	}

	return &token, nil
}
