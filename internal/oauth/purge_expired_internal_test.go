package oauth

import (
	"testing"
	"time"
)

// PurgeExpired's own doc comment: "Call periodically ... to prevent
// unbounded map growth" — an unbounded-memory-growth guard across three
// independent maps (auth codes, agent registrations + their claim-token
// index, agent claims), previously untested directly.
func TestPurgeExpiredRemovesOnlyExpiredEntries(t *testing.T) {
	svc, _ := newAgentTestService()
	now := time.Now()

	svc.codes["expired-code"] = authCode{ExpiresAt: now.Add(-time.Minute)}
	svc.codes["live-code"] = authCode{ExpiresAt: now.Add(time.Hour)}

	svc.agentRegs["expired-assertion"] = agentRegistration{
		AssertionExpires: now.Add(-time.Minute), ClaimToken: "expired-claim-token",
	}
	svc.agentClaimTokens["expired-claim-token"] = "expired-assertion"
	svc.agentRegs["live-assertion"] = agentRegistration{
		AssertionExpires: now.Add(time.Hour), ClaimToken: "live-claim-token",
	}
	svc.agentClaimTokens["live-claim-token"] = "live-assertion"

	svc.agentClaims["expired-attempt"] = agentClaim{ExpiresAt: now.Add(-time.Minute)}
	svc.agentClaims["live-attempt"] = agentClaim{ExpiresAt: now.Add(time.Hour)}

	svc.PurgeExpired()

	if _, ok := svc.codes["expired-code"]; ok {
		t.Error("PurgeExpired left an expired auth code in place")
	}
	if _, ok := svc.codes["live-code"]; !ok {
		t.Error("PurgeExpired removed a live auth code")
	}

	if _, ok := svc.agentRegs["expired-assertion"]; ok {
		t.Error("PurgeExpired left an expired agent registration in place")
	}
	if _, ok := svc.agentClaimTokens["expired-claim-token"]; ok {
		t.Error("PurgeExpired left the expired registration's claim-token index entry behind")
	}
	if _, ok := svc.agentRegs["live-assertion"]; !ok {
		t.Error("PurgeExpired removed a live agent registration")
	}
	if _, ok := svc.agentClaimTokens["live-claim-token"]; !ok {
		t.Error("PurgeExpired removed a live registration's claim-token index entry")
	}

	if _, ok := svc.agentClaims["expired-attempt"]; ok {
		t.Error("PurgeExpired left an expired agent claim in place")
	}
	if _, ok := svc.agentClaims["live-attempt"]; !ok {
		t.Error("PurgeExpired removed a live agent claim")
	}
}
