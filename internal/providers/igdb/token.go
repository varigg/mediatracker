package igdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"
)

const defaultTokenURL = "https://id.twitch.tv/oauth2/token"

// tokenSource caches a Twitch client-credentials token and refreshes it
// one minute before expiry.
type tokenSource struct {
	clientID     string
	clientSecret string
	tokenURL     string
	httpClient   *http.Client
	now          func() time.Time

	mu        sync.Mutex
	token     string
	expiresAt time.Time
}

func (t *tokenSource) get(ctx context.Context) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.token != "" && t.now().Before(t.expiresAt.Add(-time.Minute)) {
		return t.token, nil
	}
	params := url.Values{
		"client_id":     {t.clientID},
		"client_secret": {t.clientSecret},
		"grant_type":    {"client_credentials"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.tokenURL+"?"+params.Encode(), nil)
	if err != nil {
		return "", fmt.Errorf("igdb: build token request: %w", err)
	}
	resp, err := t.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("igdb: token: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("igdb: token endpoint returned %s", resp.Status)
	}
	var body struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("igdb: decode token: %w", err)
	}
	if body.AccessToken == "" {
		return "", errors.New("igdb: token endpoint returned empty access_token")
	}
	t.token = body.AccessToken
	t.expiresAt = t.now().Add(time.Duration(body.ExpiresIn) * time.Second)
	return t.token, nil
}
