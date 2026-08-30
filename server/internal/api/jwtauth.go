package api

import (
	"fmt"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/shamalawy/gdhcp/server/internal/models"
)

// Session JWT lifetimes (docs/api/endpoints.md: "Session JWT (24h TTL)").
// Refresh tokens are themselves self-contained JWTs (typ=refresh) rather
// than a server-tracked table — CONTRACTS/AMENDMENTS specify no refresh
// token store, so gdhcp validates the refresh JWT's signature+expiry+typ and
// re-checks the user is still active rather than looking anything up by ID
// (documented in notes_for_integration as a deviation: logout cannot
// server-side-revoke a still-valid JWT).
const (
	accessTokenTTL  = 24 * time.Hour
	refreshTokenTTL = 7 * 24 * time.Hour
)

const (
	tokenTypeAccess  = "access"
	tokenTypeRefresh = "refresh"
)

// sessionClaims is the JWT claim set for both access and refresh tokens
// (design/api-cli.md: "Payload includes: user_id, username, roles, iat,
// exp"; adapted to the singular `role` column per amendment A15).
type sessionClaims struct {
	jwt.RegisteredClaims
	Username string      `json:"username"`
	Role     models.Role `json:"role"`
	TokenTyp string      `json:"typ"`
}

// jwtIssuer signs and verifies session JWTs with cfg.Server.JWTSecret (HS256
// per the task spec).
type jwtIssuer struct {
	secret []byte
}

func newJWTIssuer(secret string) *jwtIssuer {
	return &jwtIssuer{secret: []byte(secret)}
}

func (j *jwtIssuer) issue(user *models.User, typ string, ttl time.Duration) (token string, expiresAt time.Time, err error) {
	now := time.Now().UTC()
	expiresAt = now.Add(ttl)
	claims := sessionClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   strconv.FormatInt(user.ID, 10),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
		},
		Username: user.Username,
		Role:     user.Role,
		TokenTyp: typ,
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := tok.SignedString(j.secret)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("api: signing jwt: %w", err)
	}
	return signed, expiresAt, nil
}

func (j *jwtIssuer) issueAccessToken(user *models.User) (string, time.Time, error) {
	return j.issue(user, tokenTypeAccess, accessTokenTTL)
}

func (j *jwtIssuer) issueRefreshToken(user *models.User) (string, time.Time, error) {
	return j.issue(user, tokenTypeRefresh, refreshTokenTTL)
}

// parse validates tokenStr's signature and expiry and, if wantTyp is
// non-empty, that its `typ` claim matches (guards against an access token
// being replayed as a refresh token or vice versa).
func (j *jwtIssuer) parse(tokenStr, wantTyp string) (*sessionClaims, error) {
	claims := &sessionClaims{}
	tok, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (any, error) {
		return j.secret, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}))
	if err != nil || !tok.Valid {
		return nil, fmt.Errorf("api: invalid token: %w", err)
	}
	if wantTyp != "" && claims.TokenTyp != wantTyp {
		return nil, fmt.Errorf("api: wrong token type: want %q got %q", wantTyp, claims.TokenTyp)
	}
	return claims, nil
}

func (c *sessionClaims) userID() (int64, error) {
	return strconv.ParseInt(c.Subject, 10, 64)
}
