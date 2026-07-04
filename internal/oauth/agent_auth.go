package oauth

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

type agentRegistration struct {
	RegistrationID   string
	AssertionToken   string
	ClaimToken       string
	AssertionExpires time.Time
	ClaimExpires     time.Time
	// Claimed is set to true only when the operator has verified the claim via
	// an out-of-band verification step. Until then the assertion cannot be
	// exchanged for a privileged token.
	// NOTE: no verification endpoint exists yet — this gate always blocks token
	// issuance, which is the secure-by-default posture. A future PR must add a
	// verification endpoint (e.g. /agent/identity/verify) that sets Claimed=true
	// after human or automated validation. See issue #27.
	Claimed bool
}

type agentClaim struct {
	RegistrationID string
	ClaimAttemptID string
	ExpiresAt      time.Time
}

type AgentClaimInfo struct {
	UserCode        string `json:"user_code"`
	ExpiresIn       int    `json:"expires_in"`
	VerificationURI string `json:"verification_uri"`
	Interval        int    `json:"interval"`
}

type AgentIdentityResponse struct {
	RegistrationID    string          `json:"registration_id"`
	RegistrationType  string          `json:"registration_type"`
	IdentityAssertion string          `json:"identity_assertion"`
	AssertionExpires  string          `json:"assertion_expires"`
	PreClaimScopes    []string        `json:"pre_claim_scopes"`
	ClaimURL          string          `json:"claim_url"`
	ClaimToken        string          `json:"claim_token"`
	ClaimTokenExpires string          `json:"claim_token_expires"`
	PostClaimScopes   []string        `json:"post_claim_scopes"`
	Claim             *AgentClaimInfo `json:"claim"`
}

type AgentClaimResponse struct {
	RegistrationID string          `json:"registration_id"`
	ClaimAttemptID string          `json:"claim_attempt_id"`
	Status         string          `json:"status"`
	ExpiresAt      string          `json:"expires_at"`
	ClaimAttempt   *AgentClaimInfo `json:"claim_attempt"`
}

func (s *Service) registerAgentAnonymous() (*AgentIdentityResponse, error) {
	issuer := s.cfg.Issuer
	regID := "reg_" + randomString(20)
	assertion := "arleo_assert_" + randomString(32)
	claimToken := "clm_" + randomString(24)
	now := time.Now()
	assertionExpires := now.Add(time.Hour)
	claimExpires := now.Add(10 * time.Minute)

	s.mu.Lock()
	s.agentRegs[assertion] = agentRegistration{
		RegistrationID:   regID,
		AssertionToken:   assertion,
		ClaimToken:       claimToken,
		AssertionExpires: assertionExpires,
		ClaimExpires:     claimExpires,
	}
	s.agentClaimTokens[claimToken] = assertion
	s.mu.Unlock()

	return &AgentIdentityResponse{
		RegistrationID:    regID,
		RegistrationType:  "anonymous",
		IdentityAssertion: assertion,
		AssertionExpires:  assertionExpires.UTC().Format(time.RFC3339Nano),
		PreClaimScopes:    []string{},
		ClaimURL:          issuer + "/agent/identity/claim",
		ClaimToken:        claimToken,
		ClaimTokenExpires: claimExpires.UTC().Format(time.RFC3339Nano),
		PostClaimScopes:   []string{"content.read"},
		Claim: &AgentClaimInfo{
			UserCode:        randomDigits(6),
			ExpiresIn:       600,
			VerificationURI: issuer + "/agent/identity/verify",
			Interval:        5,
		},
	}, nil
}

func (s *Service) exchangeAgentAssertion(assertion string) (*TokenResponse, error) {
	s.mu.RLock()
	reg, ok := s.agentRegs[assertion]
	s.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("invalid_grant")
	}
	if time.Now().After(reg.AssertionExpires) {
		return nil, fmt.Errorf("invalid_grant")
	}
	if !reg.Claimed {
		// The assertion has not been verified by a human or automated claim
		// verification step. Issuing a privileged token without claim verification
		// would allow any anonymous caller to escalate scope.
		// TODO(#27): Add a /agent/identity/verify endpoint that sets Claimed=true
		// after operator validation, then remove this guard.
		return nil, fmt.Errorf("invalid_grant: claim_required")
	}

	token := randomString(32)
	ttl := time.Duration(s.cfg.AccessTokenTTLSeconds) * time.Second
	if err := s.store.AddAccessToken(HashToken(token), "content.read", time.Now().Add(ttl)); err != nil {
		return nil, fmt.Errorf("server_error: store token: %w", err)
	}

	return &TokenResponse{
		AccessToken: token,
		TokenType:   "Bearer",
		ExpiresIn:   s.cfg.AccessTokenTTLSeconds,
	}, nil
}

func (s *Service) initiateClaim(claimToken string) (*AgentClaimResponse, error) {
	issuer := s.cfg.Issuer
	s.mu.Lock()
	defer s.mu.Unlock()

	assertionKey, ok := s.agentClaimTokens[claimToken]
	if !ok {
		return nil, fmt.Errorf("invalid_claim_token")
	}
	reg, ok := s.agentRegs[assertionKey]
	if !ok {
		return nil, fmt.Errorf("invalid_claim_token")
	}
	if time.Now().After(reg.ClaimExpires) {
		return nil, fmt.Errorf("claim_expired")
	}

	attemptID := "cla_" + randomString(20)
	expiresAt := time.Now().Add(10 * time.Minute)
	s.agentClaims[attemptID] = agentClaim{
		RegistrationID: reg.RegistrationID,
		ClaimAttemptID: attemptID,
		ExpiresAt:      expiresAt,
	}

	return &AgentClaimResponse{
		RegistrationID: reg.RegistrationID,
		ClaimAttemptID: attemptID,
		Status:         "initiated",
		ExpiresAt:      expiresAt.UTC().Format(time.RFC3339Nano),
		ClaimAttempt: &AgentClaimInfo{
			UserCode:        randomDigits(6),
			ExpiresIn:       600,
			VerificationURI: issuer + "/agent/identity/verify",
			Interval:        5,
		},
	}, nil
}

func (s *Service) HandleAgentIdentity(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Type string `json:"type"`
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 8192))
	_ = r.Body.Close()
	if err != nil || json.Unmarshal(body, &req) != nil {
		writeAgentAuthError(w, "invalid_request", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	switch req.Type {
	case "anonymous":
		resp, _ := s.registerAgentAnonymous()
		_ = json.NewEncoder(w).Encode(resp)
	default:
		writeAgentAuthError(w, "invalid_request", http.StatusBadRequest)
	}
}

func (s *Service) HandleAgentClaim(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		ClaimToken string `json:"claim_token"`
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 8192))
	_ = r.Body.Close()
	if err != nil || json.Unmarshal(body, &req) != nil || req.ClaimToken == "" {
		writeAgentAuthError(w, "invalid_request", http.StatusBadRequest)
		return
	}
	resp, err := s.initiateClaim(req.ClaimToken)
	if err != nil {
		writeAgentAuthError(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Service) HandleAgentEvent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 65536))
	_ = r.Body.Close()
	slog.Info("agent_event_notify", "content_type", r.Header.Get("Content-Type"), "body_len", len(body))
	w.WriteHeader(http.StatusOK)
}

func writeAgentAuthError(w http.ResponseWriter, code string, httpStatus int) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(httpStatus)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
}

func randomDigits(n int) string {
	const digits = "0123456789"
	b := make([]byte, n)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = digits[b[i]%10]
	}
	return string(b)
}
