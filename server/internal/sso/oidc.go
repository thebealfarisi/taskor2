package sso

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// OIDCClient wraps the OIDC provider + OAuth2 config for Keycloak.
type OIDCClient struct {
	provider *oidc.Provider
	oauth2   *oauth2.Config
	verifier *oidc.IDTokenVerifier
}

// NewOIDCClient discovers Keycloak endpoints via {issuer}/.well-known/openid-configuration.
// The issuer URL is the full Keycloak realm URL, e.g.
// "https://larasati.lintasarta.co.id/realms/dev".
func NewOIDCClient(ctx context.Context, issuer, clientID, clientSecret, redirectURL string) (*OIDCClient, error) {
	provider, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, fmt.Errorf("sso: oidc discovery failed for %q: %w", issuer, err)
	}

	return &OIDCClient{
		provider: provider,
		oauth2: &oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			Endpoint:     provider.Endpoint(),
			RedirectURL:  redirectURL,
			Scopes:       []string{oidc.ScopeOpenID, "profile", "email"},
		},
		verifier: provider.Verifier(&oidc.Config{ClientID: clientID}),
	}, nil
}

// AuthURL builds the Keycloak authorization URL with PKCE (S256) + state.
// The caller stores codeVerifier + state in a short-lived cookie and verifies
// them on the callback.
func (c *OIDCClient) AuthURL(state, codeVerifier string) string {
	return c.oauth2.AuthCodeURL(state,
		oauth2.S256ChallengeOption(codeVerifier),
	)
}

// ExchangeCode exchanges the authorization code for tokens, verifies the ID
// token signature via the OIDC discovery JWKS, and returns the email claim.
// Falls back to the userinfo endpoint if the email claim is absent from the
// ID token (some Keycloak configs omit it by default).
func (c *OIDCClient) ExchangeCode(ctx context.Context, code, codeVerifier string) (string, error) {
	token, err := c.oauth2.Exchange(ctx, code,
		oauth2.SetAuthURLParam("code_verifier", codeVerifier),
	)
	if err != nil {
		return "", fmt.Errorf("sso: token exchange failed: %w", err)
	}

	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		return "", errors.New("sso: id_token missing from token response")
	}

	idToken, err := c.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return "", fmt.Errorf("sso: id token verification failed: %w", err)
	}

	var claims struct {
		Email string `json:"email"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return "", fmt.Errorf("sso: parse id token claims: %w", err)
	}

	if claims.Email != "" {
		return claims.Email, nil
	}

	// Fallback: fetch email from the userinfo endpoint.
	userInfo, err := c.provider.UserInfo(ctx, oauth2.StaticTokenSource(token))
	if err != nil {
		return "", fmt.Errorf("sso: userinfo fetch failed: %w", err)
	}
	if err := userInfo.Claims(&claims); err != nil {
		return "", fmt.Errorf("sso: parse userinfo claims: %w", err)
	}
	if claims.Email == "" {
		return "", errors.New("sso: email claim not found in id token or userinfo")
	}
	return claims.Email, nil
}

// GenerateCodeVerifier returns a PKCE code_verifier (43-128 chars, base64url).
// See RFC 7636 §4.1.
func GenerateCodeVerifier() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("sso: generate code verifier: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// GenerateState returns a random state parameter for CSRF protection.
func GenerateState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("sso: generate state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}