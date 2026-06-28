package cliclient

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/muty/nexus/internal/model"
)

// User is the subset of the authenticated user's profile the CLI surfaces.
type User struct {
	ID        string    `json:"id"`
	Username  string    `json:"username"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

// Me returns the user the current token authenticates as. It doubles as a
// token-validity check (a bad token yields a 401 *APIError).
func (c *Client) Me(ctx context.Context) (*User, error) {
	var u User
	if err := c.do(ctx, http.MethodGet, "/api/auth/me", nil, nil, &u); err != nil {
		return nil, err
	}
	return &u, nil
}

type loginPayload struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type authResult struct {
	Token string `json:"token"`
	User  *User  `json:"user"`
}

// Login exchanges username+password for a JWT session token. The CLI uses that
// JWT only to mint a long-lived personal access token via CreateToken; it never
// persists the short-lived JWT.
func (c *Client) Login(ctx context.Context, username, password string) (string, error) {
	var res authResult
	if err := c.do(ctx, http.MethodPost, "/api/auth/login", nil, loginPayload{Username: username, Password: password}, &res); err != nil {
		return "", err
	}
	return res.Token, nil
}

type createTokenPayload struct {
	Name string `json:"name"`
}

type createTokenResult struct {
	Token string          `json:"token"`
	Meta  *model.APIToken `json:"meta"`
}

// CreateToken mints a personal access token named name. It requires the client
// to carry an interactive JWT — the /tokens route rejects API tokens — so call
// it on a client constructed with the JWT from Login. It returns the plaintext
// token (shown exactly once) and its server-side metadata.
func (c *Client) CreateToken(ctx context.Context, name string) (string, *model.APIToken, error) {
	var res createTokenResult
	if err := c.do(ctx, http.MethodPost, "/api/tokens", nil, createTokenPayload{Name: name}, &res); err != nil {
		return "", nil, fmt.Errorf("create token: %w", err)
	}
	return res.Token, res.Meta, nil
}
