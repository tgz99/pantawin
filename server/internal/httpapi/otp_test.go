//go:build integration

package httpapi_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// M6.2 exit criteria: email/password registration doesn't issue a session
// until a correct, unexpired OTP is submitted; Google sign-in bypasses OTP
// entirely (covered by TestGoogleLogin). Login on an unverified account is
// rejected distinctly from bad credentials so the client can route to the
// code-entry screen.
func TestEmailPasswordRegistrationRequiresOTP(t *testing.T) {
	env := newTestEnv(t)
	email, password := "otp@pantawin.test", "Correct-Horse-42-staple"

	// Register does NOT return a session.
	resp, err := http.Post(env.server.URL+"/v1/auth/register", "application/json",
		strings.NewReader(fmt.Sprintf(`{"email":%q,"password":%q}`, email, password)))
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 from register, got %d", resp.StatusCode)
	}
	var registerBody map[string]string
	json.NewDecoder(resp.Body).Decode(&registerBody)
	resp.Body.Close()
	if _, hasToken := registerBody["access_token"]; hasToken {
		t.Fatal("register response should not contain a session token")
	}
	if registerBody["status"] != "verification_required" {
		t.Fatalf("expected status=verification_required, got %v", registerBody)
	}

	// Login before verification is rejected distinctly (428), not 401.
	resp = env.do(t, http.MethodPost, "/v1/auth/login", "", map[string]any{"email": email, "password": password})
	if resp.StatusCode != http.StatusPreconditionRequired {
		t.Fatalf("login on unverified account: expected 428, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Wrong code rejected, doesn't consume the real one.
	resp = env.do(t, http.MethodPost, "/v1/auth/verify-otp", "", map[string]any{"email": email, "code": "000000"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("wrong otp: expected 400, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Correct code issues a session.
	code := env.otp.last(email)
	if code == "" {
		t.Fatal("no otp captured")
	}
	resp = env.do(t, http.MethodPost, "/v1/auth/verify-otp", "", map[string]any{"email": email, "code": code})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("correct otp: expected 200, got %d", resp.StatusCode)
	}
	var tk tokens
	json.NewDecoder(resp.Body).Decode(&tk)
	resp.Body.Close()
	if tk.AccessToken == "" {
		t.Fatal("expected access token from verify-otp")
	}

	// The code is single-use: replaying it now fails.
	resp = env.do(t, http.MethodPost, "/v1/auth/verify-otp", "", map[string]any{"email": email, "code": code})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("replayed otp: expected 400, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Now verified, login works normally.
	resp = env.do(t, http.MethodPost, "/v1/auth/login", "", map[string]any{"email": email, "password": password})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login after verification: expected 200, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestOTPResendAndCooldown(t *testing.T) {
	env := newTestEnv(t)
	email, password := "resend@pantawin.test", "Correct-Horse-42-staple"

	resp, err := http.Post(env.server.URL+"/v1/auth/register", "application/json",
		strings.NewReader(fmt.Sprintf(`{"email":%q,"password":%q}`, email, password)))
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	resp.Body.Close()
	firstCode := env.otp.last(email)

	// Immediate resend is rate-limited (test env cooldown = 300ms).
	resp = env.do(t, http.MethodPost, "/v1/auth/resend-otp", "", map[string]any{"email": email})
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("immediate resend: expected 429, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	time.Sleep(350 * time.Millisecond)
	resp = env.do(t, http.MethodPost, "/v1/auth/resend-otp", "", map[string]any{"email": email})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("resend after cooldown: expected 200, got %d", resp.StatusCode)
	}
	resp.Body.Close()
	secondCode := env.otp.last(email)
	if secondCode == firstCode {
		t.Fatal("resend should issue a fresh code")
	}

	// The old code no longer works; the new one does.
	resp = env.do(t, http.MethodPost, "/v1/auth/verify-otp", "", map[string]any{"email": email, "code": firstCode})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("stale code after resend: expected 400, got %d", resp.StatusCode)
	}
	resp.Body.Close()
	resp = env.do(t, http.MethodPost, "/v1/auth/verify-otp", "", map[string]any{"email": email, "code": secondCode})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("fresh code after resend: expected 200, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Resending for an already-verified account is rejected.
	resp = env.do(t, http.MethodPost, "/v1/auth/resend-otp", "", map[string]any{"email": email})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("resend on verified account: expected 409, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Resending for an unknown email is rejected.
	resp = env.do(t, http.MethodPost, "/v1/auth/resend-otp", "", map[string]any{"email": "nobody@pantawin.test"})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("resend for unknown email: expected 404, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

// A registration retried before verifying (lost email, typo'd password)
// isn't a dead end: it resets the password and issues a fresh code rather
// than permanently locking the email as "already registered".
func TestReRegisterBeforeVerificationResendsCode(t *testing.T) {
	env := newTestEnv(t)
	email := "retry@pantawin.test"

	post := func(password string) *http.Response {
		resp, err := http.Post(env.server.URL+"/v1/auth/register", "application/json",
			strings.NewReader(fmt.Sprintf(`{"email":%q,"password":%q}`, email, password)))
		if err != nil {
			t.Fatalf("register: %v", err)
		}
		return resp
	}

	resp := post("First-Pass-42")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("first register: expected 201, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	resp = post("Second-Pass-99")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("re-register before verification: expected 201, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	code := env.otp.last(email)
	resp = env.do(t, http.MethodPost, "/v1/auth/verify-otp", "", map[string]any{"email": email, "code": code})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("verify after re-register: expected 200, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// The SECOND password is the one that took effect.
	resp = env.do(t, http.MethodPost, "/v1/auth/login", "", map[string]any{"email": email, "password": "Second-Pass-99"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login with second password: expected 200, got %d", resp.StatusCode)
	}
	resp.Body.Close()
	resp = env.do(t, http.MethodPost, "/v1/auth/login", "", map[string]any{"email": email, "password": "First-Pass-42"})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("login with stale first password: expected 401, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Once verified, re-registering the same email is a real conflict.
	resp = post("Third-Pass-77")
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("register on verified email: expected 409, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

// Google sign-in never requires OTP, including when it's linking into an
// account that registered via email/password but never finished
// verification — Google's proof of ownership supersedes the pending OTP.
func TestGoogleLoginSkipsOTPAndVerifiesPendingAccount(t *testing.T) {
	env := newTestEnv(t)
	email := "pending-google@pantawin.test"

	resp, err := http.Post(env.server.URL+"/v1/auth/register", "application/json",
		strings.NewReader(fmt.Sprintf(`{"email":%q,"password":"Correct-Horse-42-staple"}`, email)))
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	resp.Body.Close()

	// Google login succeeds immediately — no code needed.
	resp = env.do(t, http.MethodPost, "/v1/auth/google", "", map[string]any{"id_token": "google-ok:" + email})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("google login on pending account: expected 200, got %d", resp.StatusCode)
	}
	var tk tokens
	json.NewDecoder(resp.Body).Decode(&tk)
	resp.Body.Close()
	if tk.AccessToken == "" {
		t.Fatal("expected access token from google login")
	}

	// The original password login now works too — Google verified the email.
	resp = env.do(t, http.MethodPost, "/v1/auth/login", "", map[string]any{
		"email": email, "password": "Correct-Horse-42-staple",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("password login after google-verified: expected 200, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}
