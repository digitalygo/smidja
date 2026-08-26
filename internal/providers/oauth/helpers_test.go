package oauth

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/authstore"
)

func waitForBrowser(t *testing.T, ch chan string) string {
	t.Helper()
	select {
	case url := <-ch:
		return url
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for browser URL")
		return ""
	}
}

func awaitResult(t *testing.T, ch chan loginResult) loginResult {
	t.Helper()
	select {
	case result := <-ch:
		return result
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for login result")
		return loginResult{}
	}
}

func buildJWT(claims string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(claims))
	return header + "." + payload + ".sig"
}

func codexTokenJSON(accountID string) string {
	claims := `{"sub":"u1","https://api.openai.com/auth":{"chatgpt_account_id":"` + accountID + `"}}`
	token := buildJWT(claims)
	return `{"access_token":"` + token + `","refresh_token":"rt-1","expires_in":1800}`
}

type loginResult struct {
	entry authstore.Entry
	err   error
}
